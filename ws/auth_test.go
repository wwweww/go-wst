package ws

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wwweww/go-wst/internal/protocol"
)

// --- JWT helpers ---

func b64url(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func genJWT(secret, userID string) string {
	header := `{"alg":"HS256","typ":"JWT"}`
	now := time.Now().Unix()
	payload := fmt.Sprintf(`{"sub":"%s","iat":%d,"exp":%d}`, userID, now, now+86400)
	hB64 := b64url([]byte(header))
	pB64 := b64url([]byte(payload))
	input := hB64 + "." + pB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return input + "." + b64url(mac.Sum(nil))
}

// --- Inline JWT authenticator (avoids circular import) ---

type testJWTAuth struct {
	source string // "query" | "header" | "cookie"
	name   string
	secret string
}

func (a *testJWTAuth) Authenticate(_ context.Context, _ protocol.Connection, req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", fmt.Errorf("no request")
	}

	var token string
	switch a.source {
	case "query":
		token = req.HTTPRequest.URL.Query().Get(a.name)
	case "header":
		token = req.HTTPRequest.Header.Get(a.name)
		token = strings.TrimPrefix(token, "Bearer ")
	case "cookie":
		if c, err := req.HTTPRequest.Cookie(a.name); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		return "", fmt.Errorf("missing token")
	}

	// Validate HMAC-SHA256
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token")
	}
	input := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write([]byte(input))
	if !hmac.Equal(mac.Sum(nil), []byte(parts[2])) {
		// Compare base64 signature
		expectedSig := b64url(mac.Sum(nil))
		if expectedSig != parts[2] {
			return "", fmt.Errorf("invalid signature")
		}
	}

	// Decode payload
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]interface{}
	json.Unmarshal(payload, &claims)
	if sub, ok := claims["sub"].(string); ok {
		return sub, nil
	}
	return "", fmt.Errorf("no sub")
}

// --- Inline Envoy authenticator ---

type testEnvoyAuth struct{ headerName string }

func (a *testEnvoyAuth) Authenticate(_ context.Context, _ protocol.Connection, req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", fmt.Errorf("no request")
	}
	id := req.HTTPRequest.Header.Get(a.headerName)
	if id == "" {
		return "", fmt.Errorf("missing header")
	}
	return id, nil
}

// --- Test handler ---

type testHandler struct {
	connectCh chan string
	messageCh chan []byte
}

func newTestHandler() *testHandler {
	return &testHandler{
		connectCh: make(chan string, 10),
		messageCh: make(chan []byte, 10),
	}
}
func (h *testHandler) OnConnect(conn protocol.Connection) error {
	h.connectCh <- conn.UserID()
	return nil
}
func (h *testHandler) OnMessage(conn protocol.Connection, msg []byte) error {
	h.messageCh <- msg
	return nil
}
func (h *testHandler) OnDisconnect(conn protocol.Connection, err error) {}

// --- Server helper ---

func startTestAuthServer(t *testing.T, auth protocol.Authenticator) (*http.Server, *testHandler) {
	t.Helper()
	handler := newTestHandler()

	conf := WsConf{
		Addr:            "127.0.0.1:0",
		Path:            "/ws",
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		MaxConnections:  100,
		PingInterval:    30 * time.Second,
		PongWait:        60 * time.Second,
		MaxMessageSize:  65536,
		WriteTimeout:    5 * time.Second,
		Auth:            protocol.AuthConfig{Enabled: true},
	}

	srv, err := NewServer(conf, handler, WithAuthenticator(auth))
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go httpSrv.Serve(ln)

	return httpSrv, handler
}

func dialAddr(httpSrv *http.Server) string {
	return lnAddr(httpSrv)
}

func lnAddr(httpSrv *http.Server) string {
	// http.Server uses Listener internally, extract addr from listener
	// After Serve(ln), Addr() returns the listener address
	return "127.0.0.1:" + strings.Split(lnFromServer(httpSrv), ":")[1]
}

func lnFromServer(httpSrv *http.Server) string {
	// After ListenAndServe, the server's internal listener has the actual addr
	// Unfortunately not exposed, so we use the listener we created
	return ""
}

// --- Tests ---

func TestJWTAuth_QueryToken(t *testing.T) {
	secret := "test-secret-key"
	auth := &testJWTAuth{source: "query", name: "token", secret: secret}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	token := genJWT(secret, "user-query-123")
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token="+token, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case userID := <-handler.connectCh:
		if userID != "user-query-123" {
			t.Errorf("expected 'user-query-123', got '%s'", userID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestJWTAuth_HeaderBearer(t *testing.T) {
	secret := "test-secret-key"
	auth := &testJWTAuth{source: "header", name: "Authorization", secret: secret}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	token := genJWT(secret, "user-header-456")
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", hdr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case userID := <-handler.connectCh:
		if userID != "user-header-456" {
			t.Errorf("expected 'user-header-456', got '%s'", userID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestJWTAuth_Cookie(t *testing.T) {
	secret := "test-secret-key"
	auth := &testJWTAuth{source: "cookie", name: "token", secret: secret}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	token := genJWT(secret, "user-cookie-789")

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("http://" + addr)
	jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: token}})

	dialer := websocket.Dialer{Jar: jar}
	conn, _, err := dialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case userID := <-handler.connectCh:
		if userID != "user-cookie-789" {
			t.Errorf("expected 'user-cookie-789', got '%s'", userID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestJWTAuth_InvalidToken(t *testing.T) {
	auth := &testJWTAuth{source: "query", name: "token", secret: "secret"}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token=invalid.jwt.token", nil)
	if err != nil {
		t.Logf("rejected at dial (good): %v", err)
		return
	}
	defer conn.Close()

	// If dial succeeded, server should close connection shortly
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection to be closed after auth failure")
	}
	t.Logf("correctly closed: %v", err)
}

func TestJWTAuth_MissingToken(t *testing.T) {
	auth := &testJWTAuth{source: "query", name: "token", secret: "secret"}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Logf("rejected at dial (good): %v", err)
		return
	}
	defer conn.Close()

	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection to be closed without token")
	}
	t.Logf("correctly closed: %v", err)
}

func TestJWTAuth_WrongSecret(t *testing.T) {
	auth := &testJWTAuth{source: "query", name: "token", secret: "correct-secret"}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	token := genJWT("wrong-secret", "user-123")
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token="+token, nil)
	if err != nil {
		t.Logf("rejected at dial (good): %v", err)
		return
	}
	defer conn.Close()

	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection to be closed with wrong secret")
	}
	t.Logf("correctly closed: %v", err)
}

func TestEnvoyHeaderAuth(t *testing.T) {
	auth := &testEnvoyAuth{headerName: "X-User-ID"}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	hdr := http.Header{}
	hdr.Set("X-User-ID", "envoy-user-999")

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", hdr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case userID := <-handler.connectCh:
		if userID != "envoy-user-999" {
			t.Errorf("expected 'envoy-user-999', got '%s'", userID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestJWTAuth_SendReceive(t *testing.T) {
	secret := "test-secret-key"
	auth := &testJWTAuth{source: "query", name: "token", secret: secret}

	handler := newTestHandler()
	conf := WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second, PongWait: 60 * time.Second,
		MaxMessageSize: 65536, WriteTimeout: 5 * time.Second,
		Auth: protocol.AuthConfig{Enabled: true},
	}
	srv, _ := NewServer(conf, handler, WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	httpSrv := &http.Server{Handler: mux}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	token := genJWT(secret, "msg-user")
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token="+token, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	<-handler.connectCh

	msg, _ := json.Marshal(map[string]string{"type": "chat", "content": "hello"})
	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case received := <-handler.messageCh:
		var m map[string]string
		json.Unmarshal(received, &m)
		if m["content"] != "hello" {
			t.Errorf("expected 'hello', got '%s'", m["content"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}
