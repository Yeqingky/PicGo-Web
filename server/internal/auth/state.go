package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// stateKeyLabel 用于从主密钥派生 state 签名密钥（域分隔）。
const stateKeyLabel = "|picgo-web|oauth-state|v1"

// StateTTL 是 OAuth state 的有效期。超过即视为过期（防重放）。
const StateTTL = 5 * time.Minute

// StateIntent 表示这次 OAuth 流程的目的。
type StateIntent string

const (
	// StateIntentLogin 用于登录（D27：必须先绑定过才能登录）。
	StateIntentLogin StateIntent = "login"
	// StateIntentBind 用于已登录用户绑定新身份。
	StateIntentBind StateIntent = "bind"
)

var (
	// ErrStateInvalid state 签名不合法或格式错误。
	ErrStateInvalid = errors.New("state 校验失败")
	// ErrStateExpired state 已过期。
	ErrStateExpired = errors.New("state 已过期")
)

// StatePayload 是签名进 state 参数的内容。
//
// 它是**自包含**的：回调时无需任何服务端存储即可恢复上下文（无状态、可水平扩展）。
type StatePayload struct {
	// Redirect 回调成功后要跳回的**站内路径**（已做开放重定向校验）。
	Redirect string `json:"r"`
	// Intent login | bind
	Intent StateIntent `json:"i"`
	// UserUID 仅 bind 时有值，标识要绑定到哪个账号。
	UserUID string `json:"u,omitempty"`
	// Provider 绑定时用于区分 provider（当前只有 github，字段保留以便扩展）。
	Provider string `json:"p,omitempty"`
	// IssuedAt Unix 秒。
	IssuedAt int64 `json:"t"`
	// Nonce 随机值，保证同一用户每次的 state 都不同。
	Nonce string `json:"n"`
}

// StateSigner 用主密钥派生出的密钥对 state 做 HMAC-SHA256 签名。
type StateSigner struct {
	key []byte
}

// NewStateSigner 构造。
func NewStateSigner(masterKey []byte) *StateSigner {
	buf := make([]byte, 0, len(masterKey)+len(stateKeyLabel))
	buf = append(buf, masterKey...)
	buf = append(buf, stateKeyLabel...)
	sum := sha256.Sum256(buf)
	return &StateSigner{key: sum[:]}
}

// Sign 生成 `<base64url(payload)>.<base64url(hmac)>` 形式的 state。
func (s *StateSigner) Sign(p StatePayload) (string, error) {
	if p.IssuedAt == 0 {
		p.IssuedAt = time.Now().Unix()
	}
	if p.Nonce == "" {
		nonce, err := RandomString(12)
		if err != nil {
			return "", err
		}
		p.Nonce = nonce
	}

	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("编码 state 失败: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.mac(body), nil
}

// Verify 校验 state 的签名与时效，返回解出的载荷。
func (s *StateSigner) Verify(state string, maxAge time.Duration) (*StatePayload, error) {
	if state == "" {
		return nil, ErrStateInvalid
	}
	idx := strings.LastIndex(state, ".")
	if idx <= 0 || idx == len(state)-1 {
		return nil, ErrStateInvalid
	}
	body, sig := state[:idx], state[idx+1:]

	// 等时比较，避免签名被逐字节爆破
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.mac(body))) != 1 {
		return nil, ErrStateInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrStateInvalid
	}
	var p StatePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrStateInvalid
	}
	if p.Intent != StateIntentLogin && p.Intent != StateIntentBind {
		return nil, ErrStateInvalid
	}

	if maxAge <= 0 {
		maxAge = StateTTL
	}
	if time.Since(time.Unix(p.IssuedAt, 0)) > maxAge {
		return nil, ErrStateExpired
	}
	return &p, nil
}

func (s *StateSigner) mac(body string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// SanitizeRedirect 校验并规范化「回调后跳回的路径」，防开放重定向。
//
// 规则：必须是**站内相对路径**（以单个 `/` 开头），不得含 `//`（协议相对）、
// 反斜杠、控制字符或 `..`；非法时返回 fallback。
func SanitizeRedirect(raw, fallback string) string {
	if fallback == "" {
		fallback = "/"
	}
	v := strings.TrimSpace(raw)
	if v == "" {
		return fallback
	}
	// 只取路径部分，丢掉可能的 origin
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return fallback
	}
	if strings.ContainsAny(v, "\\\r\n\t") {
		return fallback
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == ".." {
			return fallback
		}
	}
	return v
}
