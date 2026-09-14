package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// APITokenPrefix 是长期令牌的固定前缀，便于识别与检索（D31）。
const APITokenPrefix = "pcw_"

// refreshTokenBytes refresh token 的随机字节数（32 字节 = 256 位）。
const refreshTokenBytes = 32

// apiTokenBytes API token 的随机字节数。
const apiTokenBytes = 32

// apiTokenPrefixLen Prefix 字段展示的长度：`pcw_` + 8 个随机字符。
const apiTokenPrefixLen = len(APITokenPrefix) + 8

// HashToken 计算令牌的 SHA-256 十六进制摘要。
//
// ⚠️ 数据库里**只存摘要**（D30/D31）：即使库被读走，也无法反推出可用令牌。
// 令牌是 256 位随机值，不存在被暴力破解的风险，因此不需要加盐或慢哈希
// （bcrypt 反而会让每个请求都要跑一次重哈希）。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// GenerateRefreshToken 生成 refresh token 的明文与摘要。
func GenerateRefreshToken() (plain, hash string, err error) {
	plain, err = RandomString(refreshTokenBytes)
	if err != nil {
		return "", "", err
	}
	return plain, HashToken(plain), nil
}

// GenerateAPIToken 生成 API token 的明文、摘要与展示用前缀。
//
// 明文形如 `pcw_<43 个 base64url 字符>`。
func GenerateAPIToken() (plain, hash, prefix string, err error) {
	body, err := RandomString(apiTokenBytes)
	if err != nil {
		return "", "", "", err
	}
	plain = APITokenPrefix + body
	hash = HashToken(plain)
	prefix = plain[:apiTokenPrefixLen]
	return plain, hash, prefix, nil
}

// IsAPIToken 判断一个 Bearer 令牌是否为 API token（据此选择校验路径）。
func IsAPIToken(token string) bool {
	return len(token) > len(APITokenPrefix) && token[:len(APITokenPrefix)] == APITokenPrefix
}
