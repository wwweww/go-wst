package hybrid

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/wwweww/go-wst/internal/protocol"
)

var (
	ErrMissingToken    = errors.New("missing authentication token")
	ErrInvalidToken    = errors.New("invalid authentication token")
	ErrMissingUserInfo = errors.New("missing user info from envoy")
)

// HybridAuthenticator 混合认证器，支持 WebSocket(Envoy Header) 和 WebTransport(JWT)
type HybridAuthenticator struct {
	wsConfig WsAuthConfig
	wtConfig WtAuthConfig
}

// WsAuthConfig WebSocket 认证配置
type WsAuthConfig struct {
	// Source: envoy-header（信任 Envoy 传递的用户信息）
	Source string
	// HeaderName Envoy 设置的用户 ID Header
	HeaderName string
}

// WtAuthConfig WebTransport 认证配置
type WtAuthConfig struct {
	// TokenSource: query | header
	TokenSource string
	// TokenName token 参数名
	TokenName string
	// JWTSecret JWT 密钥
	JWTSecret string
}

// NewHybridAuthenticator 创建混合认证器
func NewHybridAuthenticator(wsConfig WsAuthConfig, wtConfig WtAuthConfig) *HybridAuthenticator {
	return &HybridAuthenticator{
		wsConfig: wsConfig,
		wtConfig: wtConfig,
	}
}

// Authenticate 实现 Authenticator 接口
func (a *HybridAuthenticator) Authenticate(ctx context.Context, conn protocol.Connection, req *protocol.AuthRequest) (string, error) {
	switch req.Protocol {
	case protocol.ProtocolWebSocket:
		return a.authenticateFromEnvoy(req)
	case protocol.ProtocolWebTransport:
		return a.authenticateJWT(req)
	default:
		return "", errors.New("unsupported protocol")
	}
}

// authenticateFromEnvoy 从 Envoy 传递的 Header 读取已验证用户
func (a *HybridAuthenticator) authenticateFromEnvoy(req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", ErrMissingUserInfo
	}

	userID := req.HTTPRequest.Header.Get(a.wsConfig.HeaderName)
	if userID == "" {
		return "", ErrMissingUserInfo
	}

	return userID, nil
}

// authenticateJWT 自己验证 JWT
func (a *HybridAuthenticator) authenticateJWT(req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", ErrMissingToken
	}

	var token string

	switch a.wtConfig.TokenSource {
	case "query":
		token = req.HTTPRequest.URL.Query().Get(a.wtConfig.TokenName)
	case "header":
		token = req.HTTPRequest.Header.Get(a.wtConfig.TokenName)
		// 支持 Bearer token 格式
		if strings.HasPrefix(token, "Bearer ") {
			token = strings.TrimPrefix(token, "Bearer ")
		}
	case "cookie":
		if cookie, err := req.HTTPRequest.Cookie(a.wtConfig.TokenName); err == nil {
			token = cookie.Value
		}
	}

	if token == "" {
		return "", ErrMissingToken
	}

	// 解析 JWT
	claims, err := jwt.ParseWithClaims(token, &jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(a.wtConfig.JWTSecret), nil
	})

	if err != nil {
		return "", ErrInvalidToken
	}

	if !claims.Valid {
		return "", ErrInvalidToken
	}

	// 提取用户 ID
	mapClaims := claims.Claims.(*jwt.MapClaims)

	// 尝试常见字段名，支持 string 和 numeric 类型
	for _, field := range []string{"sub", "user_id", "UserId", "userId", "uid"} {
		if val, ok := (*mapClaims)[field]; ok {
			switch v := val.(type) {
			case string:
				if v != "" {
					return v, nil
				}
			case float64:
				return strconv.FormatInt(int64(v), 10), nil
			case json.Number:
				return v.String(), nil
			}
		}
	}

	return "", ErrInvalidToken
}

// JWTAuthenticator 简单的 JWT 认证器（用于单一协议）
type JWTAuthenticator struct {
	tokenSource string
	tokenName   string
	secret      string
	timeout     time.Duration
}

// NewJWTAuthenticator 创建 JWT 认证器
func NewJWTAuthenticator(tokenSource, tokenName, secret string, timeout time.Duration) *JWTAuthenticator {
	return &JWTAuthenticator{
		tokenSource: tokenSource,
		tokenName:   tokenName,
		secret:      secret,
		timeout:     timeout,
	}
}

// Authenticate 实现 Authenticator 接口
func (a *JWTAuthenticator) Authenticate(ctx context.Context, conn protocol.Connection, req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", ErrMissingToken
	}

	var token string

	switch a.tokenSource {
	case "query":
		token = req.HTTPRequest.URL.Query().Get(a.tokenName)
	case "header":
		token = req.HTTPRequest.Header.Get(a.tokenName)
		if strings.HasPrefix(token, "Bearer ") {
			token = strings.TrimPrefix(token, "Bearer ")
		}
	case "cookie":
		if cookie, err := req.HTTPRequest.Cookie(a.tokenName); err == nil {
			token = cookie.Value
		}
	}

	if token == "" {
		return "", ErrMissingToken
	}

	claims, err := jwt.ParseWithClaims(token, &jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(a.secret), nil
	})

	if err != nil || !claims.Valid {
		return "", ErrInvalidToken
	}

	mapClaims := claims.Claims.(*jwt.MapClaims)
	if sub, ok := (*mapClaims)["sub"]; ok {
		if userID, ok := sub.(string); ok {
			return userID, nil
		}
	}

	return "", ErrInvalidToken
}

// EnvoyHeaderAuthenticator 从 Envoy Header 读取用户信息
type EnvoyHeaderAuthenticator struct {
	headerName string
}

// NewEnvoyHeaderAuthenticator 创建 Envoy Header 认证器
func NewEnvoyHeaderAuthenticator(headerName string) *EnvoyHeaderAuthenticator {
	return &EnvoyHeaderAuthenticator{
		headerName: headerName,
	}
}

// Authenticate 实现 Authenticator 接口
func (a *EnvoyHeaderAuthenticator) Authenticate(ctx context.Context, conn protocol.Connection, req *protocol.AuthRequest) (string, error) {
	if req.HTTPRequest == nil {
		return "", ErrMissingUserInfo
	}

	userID := req.HTTPRequest.Header.Get(a.headerName)
	if userID == "" {
		return "", ErrMissingUserInfo
	}

	return userID, nil
}
