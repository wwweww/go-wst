package protocol

import (
	"context"
	"net/http"
)

// Authenticator 认证器接口
type Authenticator interface {
	// Authenticate 验证连接
	// 返回 userID 表示认证成功，返回 error 表示认证失败（连接将被拒绝）
	Authenticate(ctx context.Context, conn Connection, req *AuthRequest) (userID string, err error)
}

// AuthRequest 认证请求信息
type AuthRequest struct {
	// Protocol 协议类型
	Protocol Protocol

	// HTTPRequest WebSocket 升级时的 HTTP 请求（包含 Header、Query、Cookie）
	// WebTransport 同样有初始 HTTP 请求
	HTTPRequest *http.Request

	// FirstMessage 首条消息（仅 first-message 模式）
	FirstMessage []byte
}

// AuthMode 认证模式
type AuthMode string

const (
	// AuthModeHeader 从 HTTP Header/Query 提取 token（推荐）
	AuthModeHeader AuthMode = "header"

	// AuthModeFirstMessage 客户端连接后首条消息包含认证信息
	AuthModeFirstMessage AuthMode = "first-message"
)

// AuthSource 认证来源
type AuthSource string

const (
	// AuthSourceEnvoyHeader 从 Envoy 传递的 Header 读取已验证用户
	AuthSourceEnvoyHeader AuthSource = "envoy-header"

	// AuthSourceJWT 自己验证 JWT
	AuthSourceJWT AuthSource = "jwt"
)

// AuthConfig 认证配置
type AuthConfig struct {
	// Enabled 是否启用认证
	Enabled bool `json:",default=false"`

	// Mode 认证模式: header | first-message
	Mode AuthMode `json:",default=header"`

	// Source 认证来源: envoy-header | jwt
	Source AuthSource `json:",default=jwt"`

	// TokenSource token 来源: header | query | cookie
	TokenSource string `json:",default=query"`

	// TokenName token 名称（header key / query param / cookie name）
	TokenName string `json:",default=token"`

	// HeaderName 从 Header 读取用户信息时的 Header 名称（Envoy 模式）
	HeaderName string `json:",optional"`

	// JWTSecret JWT 密钥
	JWTSecret string `json:",optional"`

	// Timeout first-message 模式的超时时间
	Timeout int64 `json:",default=5"` // seconds
}

// NoOpAuthenticator 空认证器（不验证）
type NoOpAuthenticator struct{}

// Authenticate 总是返回成功
func (a *NoOpAuthenticator) Authenticate(ctx context.Context, conn Connection, req *AuthRequest) (string, error) {
	return "", nil
}
