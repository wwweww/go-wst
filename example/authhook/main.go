package main

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
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wwweww/go-wst/hybrid"
	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/wwweww/go-wst/ws"
)

var (
	passed int
	failed int
	mu     sync.Mutex
)

func ok(name string) {
	mu.Lock()
	passed++
	fmt.Printf("  PASS  %s\n", name)
	mu.Unlock()
}

func fail(name, detail string) {
	mu.Lock()
	failed++
	fmt.Printf("  FAIL  %s  --  %s\n", name, detail)
	mu.Unlock()
}

// ---- JWT ----

func b64url(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func genJWT(secret, userID string) string {
	h := `{"alg":"HS256","typ":"JWT"}`
	now := time.Now().Unix()
	p := fmt.Sprintf(`{"sub":"%s","iat":%d,"exp":%d}`, userID, now, now+86400)
	hB := b64url([]byte(h))
	pB := b64url([]byte(p))
	input := hB + "." + pB
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return input + "." + b64url(mac.Sum(nil))
}

// ---- handler ----

type h struct {
	connects []string
	messages []string
	sync.Mutex
}

func (h *h) OnConnect(c protocol.Connection) error {
	h.Lock()
	h.connects = append(h.connects, c.UserID())
	h.Unlock()
	return nil
}
func (h *h) OnMessage(c protocol.Connection, m []byte) error {
	h.Lock()
	h.messages = append(h.messages, string(m))
	h.Unlock()
	return nil
}
func (h *h) OnDisconnect(c protocol.Connection, err error) {}

func (h *h) lastUID() string {
	h.Lock()
	defer h.Unlock()
	if len(h.connects) == 0 {
		return ""
	}
	return h.connects[len(h.connects)-1]
}

func (h *h) waitConn(n int, t time.Duration) bool {
	dl := time.Now().Add(t)
	for time.Now().Before(dl) {
		h.Lock()
		got := len(h.connects)
		h.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ---- server ----

func serve(auth protocol.Authenticator) (string, *h, func()) {
	handler := &h{}
	conf := ws.WsConf{
		Addr: "127.0.0.1:0", Path: "/ws",
		ReadBufferSize: 1024, WriteBufferSize: 1024,
		MaxConnections: 100, PingInterval: 30 * time.Second,
		PongWait: 60 * time.Second, MaxMessageSize: 65536,
		WriteTimeout: 5 * time.Second,
		Auth:         protocol.AuthConfig{Enabled: true},
	}
	srv, _ := ws.NewServer(conf, handler, ws.WithAuthenticator(auth))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.HandleWebSocket)
	hs := &http.Server{Handler: mux}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go hs.Serve(ln)
	return ln.Addr().String(), handler, func() { hs.Close() }
}

func dialWS(url string, hdr http.Header) (*websocket.Conn, error) {
	c, _, err := websocket.DefaultDialer.Dial(url, hdr)
	return c, err
}

func readMsg(c *websocket.Conn, t time.Duration) ([]byte, error) {
	c.SetReadDeadline(time.Now().Add(t))
	_, m, err := c.ReadMessage()
	return m, err
}

// ============================================================

func main() {
	secret := "test-secret"
	fmt.Println("========================================")
	fmt.Println(" go-wst Auth Integration Test")
	fmt.Println("========================================\n")

	// 1. JWT via query
	fmt.Println("[1] JWT via query param")
	addr, rec, close := serve(hybrid.NewJWTAuthenticator("query", "token", secret, 5*time.Second))

	token := genJWT(secret, "query-user")
	c, err := dialWS("ws://"+addr+"/ws?token="+token, nil)
	if err != nil {
		fail("valid token connects", err.Error())
	} else {
		c.Close()
		rec.waitConn(1, 2*time.Second)
		if rec.lastUID() == "query-user" {
			ok("valid token connects → userID=query-user")
		} else {
			fail("valid token connects", "got userID="+rec.lastUID())
		}
	}

	c, _ = dialWS("ws://"+addr+"/ws?token=garbage", nil)
	if c != nil {
		_, err = readMsg(c, 2*time.Second)
		c.Close()
	}
	if err != nil || c == nil {
		ok("invalid token rejected")
	} else {
		fail("invalid token rejected", "connection should close")
	}

	c, _ = dialWS("ws://"+addr+"/ws", nil)
	if c != nil {
		_, err = readMsg(c, 2*time.Second)
		c.Close()
	}
	if err != nil || c == nil {
		ok("missing token rejected")
	} else {
		fail("missing token rejected", "connection should close")
	}

	badToken := genJWT("wrong-secret", "x")
	c, _ = dialWS("ws://"+addr+"/ws?token="+badToken, nil)
	if c != nil {
		_, err = readMsg(c, 2*time.Second)
		c.Close()
	}
	if err != nil || c == nil {
		ok("wrong secret rejected")
	} else {
		fail("wrong secret rejected", "connection should close")
	}
	close()
	fmt.Println()

	// 2. JWT via Authorization: Bearer
	fmt.Println("[2] JWT via Authorization: Bearer")
	addr, rec, close = serve(hybrid.NewJWTAuthenticator("header", "Authorization", secret, 5*time.Second))

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+genJWT(secret, "header-user"))
	c, err = dialWS("ws://"+addr+"/ws", hdr)
	if err != nil {
		fail("bearer token connects", err.Error())
	} else {
		c.Close()
		rec.waitConn(1, 2*time.Second)
		if rec.lastUID() == "header-user" {
			ok("bearer token connects → userID=header-user")
		} else {
			fail("bearer token connects", "got userID="+rec.lastUID())
		}
	}

	hdr2 := http.Header{}
	hdr2.Set("Authorization", genJWT(secret, "raw-user"))
	c, err = dialWS("ws://"+addr+"/ws", hdr2)
	if err != nil {
		fail("raw token in header", err.Error())
	} else {
		c.Close()
		rec.waitConn(2, 2*time.Second)
		if rec.lastUID() == "raw-user" {
			ok("raw token in header → userID=raw-user")
		} else {
			fail("raw token in header", "got userID="+rec.lastUID())
		}
	}
	close()
	fmt.Println()

	// 3. JWT via cookie
	fmt.Println("[3] JWT via cookie")
	addr, rec, close = serve(hybrid.NewJWTAuthenticator("cookie", "token", secret, 5*time.Second))

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("http://" + addr)
	jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: genJWT(secret, "cookie-user")}})
	d := websocket.Dialer{Jar: jar}
	c, _, err = d.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		fail("cookie token connects", err.Error())
	} else {
		c.Close()
		rec.waitConn(1, 2*time.Second)
		if rec.lastUID() == "cookie-user" {
			ok("cookie token connects → userID=cookie-user")
		} else {
			fail("cookie token connects", "got userID="+rec.lastUID())
		}
	}
	close()
	fmt.Println()

	// 4. Envoy header
	fmt.Println("[4] Envoy X-User-ID header")
	addr, rec, close = serve(hybrid.NewEnvoyHeaderAuthenticator("X-User-ID"))

	hdr3 := http.Header{}
	hdr3.Set("X-User-ID", "envoy-user")
	c, err = dialWS("ws://"+addr+"/ws", hdr3)
	if err != nil {
		fail("envoy header connects", err.Error())
	} else {
		c.Close()
		rec.waitConn(1, 2*time.Second)
		if rec.lastUID() == "envoy-user" {
			ok("envoy header connects → userID=envoy-user")
		} else {
			fail("envoy header connects", "got userID="+rec.lastUID())
		}
	}

	c, _ = dialWS("ws://"+addr+"/ws", nil)
	if c != nil {
		_, err = readMsg(c, 2*time.Second)
		c.Close()
	}
	if err != nil || c == nil {
		ok("missing envoy header rejected")
	} else {
		fail("missing envoy header rejected", "should close")
	}
	close()
	fmt.Println()

	// 5. Send/receive
	fmt.Println("[5] Send & receive after auth")
	addr, rec, close = serve(hybrid.NewJWTAuthenticator("query", "token", secret, 5*time.Second))

	c, err = dialWS("ws://"+addr+"/ws?token="+genJWT(secret, "msg-user"), nil)
	if err != nil {
		fail("connect for send/receive", err.Error())
	} else {
		rec.waitConn(1, 2*time.Second)
		msg, _ := json.Marshal(map[string]string{"type": "chat", "content": "hello"})
		c.WriteMessage(websocket.TextMessage, msg)
		time.Sleep(200 * time.Millisecond)
		rec.Lock()
		nMsg := len(rec.messages)
		rec.Unlock()
		if nMsg > 0 {
			var m map[string]string
			json.Unmarshal([]byte(rec.messages[0]), &m)
			if m["content"] == "hello" {
				ok("message received → content=hello")
			} else {
				fail("message content", "got "+m["content"])
			}
		} else {
			fail("message received", "no message arrived")
		}
		c.Close()
	}
	close()
	fmt.Println()

	// 6. Multi-user
	fmt.Println("[6] Concurrent multi-user")
	addr, rec, close = serve(hybrid.NewJWTAuthenticator("query", "token", secret, 5*time.Second))

	var conns []*websocket.Conn
	allOk := true
	for i := 0; i < 10; i++ {
		c, err := dialWS("ws://"+addr+"/ws?token="+genJWT(secret, fmt.Sprintf("u%d", i)), nil)
		if err != nil {
			fail("10 concurrent users", fmt.Sprintf("user %d: %v", i, err))
			allOk = false
			break
		}
		conns = append(conns, c)
	}
	if allOk {
		rec.waitConn(10, 3*time.Second)
		rec.Lock()
		n := len(rec.connects)
		rec.Unlock()
		if n >= 10 {
			ok(fmt.Sprintf("10 concurrent users connected (%d)", n))
		} else {
			fail("10 concurrent users", fmt.Sprintf("only %d connected", n))
		}
	}
	for _, c := range conns {
		c.Close()
	}
	close()
	fmt.Println()

	// 7. HybridAuthenticator routing
	fmt.Println("[7] HybridAuthenticator protocol routing")
	ha := hybrid.NewHybridAuthenticator(
		hybrid.WsAuthConfig{Source: "envoy-header", HeaderName: "X-User-ID"},
		hybrid.WtAuthConfig{TokenSource: "query", TokenName: "token", JWTSecret: secret},
	)

	r1, _ := http.NewRequest("GET", "/ws", nil)
	r1.Header.Set("X-User-ID", "ws-envoy")
	uid, _ := ha.Authenticate(context.Background(), nil, &protocol.AuthRequest{Protocol: protocol.ProtocolWebSocket, HTTPRequest: r1})
	if uid == "ws-envoy" {
		ok("hybrid WS → envoy header")
	} else {
		fail("hybrid WS → envoy header", "got "+uid)
	}

	r2, _ := http.NewRequest("GET", "/wt?token="+genJWT(secret, "wt-jwt"), nil)
	uid2, _ := ha.Authenticate(context.Background(), nil, &protocol.AuthRequest{Protocol: protocol.ProtocolWebTransport, HTTPRequest: r2})
	if uid2 == "wt-jwt" {
		ok("hybrid WT → JWT")
	} else {
		fail("hybrid WT → JWT", "got "+uid2)
	}

	_, err7 := ha.Authenticate(context.Background(), nil, &protocol.AuthRequest{Protocol: "unknown", HTTPRequest: r1})
	if err7 != nil {
		ok("hybrid unknown protocol rejected")
	} else {
		fail("hybrid unknown protocol", "should error")
	}
	fmt.Println()

	// Summary
	fmt.Println("========================================")
	total := passed + failed
	if failed == 0 {
		fmt.Printf(" ALL %d TESTS PASSED\n", total)
	} else {
		fmt.Printf(" %d / %d FAILED\n", failed, total)
	}
	fmt.Println("========================================")
}
