package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTypeAccess 是 access token 的 typ claim 取值。
const TokenTypeAccess = "access"

// jwtKeyLabel 用于从主密钥派生 JWT 签名密钥（域分隔，避免与其它用途共用同一密钥）。
const jwtKeyLabel = "|picgo-web|jwt|v1"

var (
	// ErrTokenExpired 令牌已过期（映射 40103）。
	ErrTokenExpired = errors.New("令牌已过期")
	// ErrTokenInvalid 令牌无效（签名错误 / 格式错误 / typ 不符，映射 40102）。
	ErrTokenInvalid = errors.New("令牌无效")
)

// Claims 是 access token 的载荷。
type Claims struct {
	// Role 冗余在令牌里，便于快速判断而无需查库；
	// 但**授权判断仍以数据库为准**（角色可能已被改），见 middleware/auth.go。
	Role string `json:"role"`
	// Typ 固定为 "access"，防止把其它类型的 JWT 当作 access token 使用。
	Typ string `json:"typ"`
	jwt.RegisteredClaims
}

// JWTManager 负责签发与校验 access token。
//
// 签名密钥**从主密钥派生**（D19 精神：主密钥是唯一秘密，不另存 secret）。
type JWTManager struct {
	key []byte
}

// NewJWTManager 用主密钥派生签名密钥。
func NewJWTManager(masterKey []byte) *JWTManager {
	buf := make([]byte, 0, len(masterKey)+len(jwtKeyLabel))
	buf = append(buf, masterKey...)
	buf = append(buf, jwtKeyLabel...)
	sum := sha256.Sum256(buf)
	return &JWTManager{key: sum[:]}
}

// Sign 签发 access token，返回令牌与其有效秒数。
func (m *JWTManager) Sign(userUID, role string, ttl time.Duration) (string, int64, error) {
	if userUID == "" {
		return "", 0, errors.New("签发令牌失败：用户 UID 为空")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	now := time.Now()
	claims := Claims{
		Role: role,
		Typ:  TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userUID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.key)
	if err != nil {
		return "", 0, fmt.Errorf("签发令牌失败: %w", err)
	}
	return signed, int64(ttl.Seconds()), nil
}

// Verify 校验 access token。
//
// 返回 ErrTokenExpired / ErrTokenInvalid 以区分 40103 与 40102。
func (m *JWTManager) Verify(token string) (*Claims, error) {
	if token == "" {
		return nil, ErrTokenInvalid
	}

	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims,
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, ErrTokenInvalid
			}
			return m.key, nil
		},
		// 只接受 HS256，防算法混淆（alg=none / RS256 混淆攻击）
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	if !parsed.Valid {
		return nil, ErrTokenInvalid
	}
	if claims.Typ != TokenTypeAccess || claims.Subject == "" {
		return nil, ErrTokenInvalid
	}
	return &claims, nil
}
