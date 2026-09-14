// Package crypto 提供 AES-256-GCM 加解密与主密钥管理。
//
// 用途：加密存储在数据库中的敏感配置（SMTP 密码、OAuth client secret、
// 存储驱动的 token / secret / 密码）。
//
// ⚠️ 主密钥**绝不进数据库**（D19）：它用于解密库中的密文，
// 与密文同库存储等于没有加密。来源优先级：
//
//  1. 环境变量 PICGO_WEB_SECRET_KEY（容器推荐）
//  2. <dataDir>/secret.key（首次启动生成，权限 0600）
//
// 密文格式（自描述，便于将来轮换）：
//
//	base64( version(1B) || nonce(12B) || ciphertext+tag )
//
// version 当前为 1；解密时按 version 选择算法，因此可在不改调用方的前提下升级。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// KeySize AES-256 的密钥长度。
const KeySize = 32

// nonceSize GCM 标准 nonce 长度。
const nonceSize = 12

// versionV1 当前密文格式版本。
const versionV1 byte = 1

var (
	// ErrKeyMissing 表示主密钥为空。
	ErrKeyMissing = errors.New("加密主密钥为空")
	// ErrKeyInvalid 表示主密钥长度不对。
	ErrKeyInvalid = fmt.Errorf("加密主密钥长度必须为 %d 字节", KeySize)
	// ErrCipherInvalid 表示密文格式非法。
	ErrCipherInvalid = errors.New("密文格式非法")
	// ErrDecrypt 表示解密失败（密钥不匹配或数据被篡改）。
	ErrDecrypt = errors.New("解密失败：密钥不匹配或数据已被篡改")
	// ErrUnsupportedVersion 表示密文版本不受支持。
	ErrUnsupportedVersion = errors.New("不支持的密文版本")
)

// Cipher 持有主密钥，提供加解密能力。
//
// 并发安全（AEAD 是只读的，可被多 goroutine 共用）。
type Cipher struct {
	key   []byte
	nonce []byte // 测试用固定 nonce；生产为 nil（每次随机）
}

// New 用给定主密钥构造 Cipher。
// key 必须是 32 字节（AES-256）。
func New(key []byte) (*Cipher, error) {
	if len(key) == 0 {
		return nil, ErrKeyMissing
	}
	if len(key) != KeySize {
		return nil, ErrKeyInvalid
	}
	k := make([]byte, KeySize)
	copy(k, key)
	return &Cipher{key: k}, nil
}

// NewFromString 从字符串构造 Cipher。
//
// 支持两种形式：
//   - 64 个十六进制字符（32 字节）
//   - 44 字符左右的 base64（32 字节）
//   - 其它长度原样按字节使用（要求恰好 32 字节）
func NewFromString(s string) (*Cipher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrKeyMissing
	}

	// 优先按原样字节（32 字节）
	if len(s) == KeySize {
		return New([]byte(s))
	}

	if len(s) == KeySize*2 && isHex(s) {
		b, err := decodeHex(s)
		if err != nil {
			return nil, err
		}
		return New(b)
	}

	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == KeySize {
		return New(b)
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(b) == KeySize {
		return New(b)
	}

	return nil, ErrKeyInvalid
}

// LoadOrCreateKey 按优先级获取主密钥：
//
//  1. fromEnv 非空 → 直接用它（不落盘）
//  2. keyFile 存在 → 读取
//  3. 都不存在 → 生成随机密钥并写入 keyFile（权限 0600）
//
// 返回的第二个值表示是否**新生成**了密钥（调用方通常需要打日志提醒备份）。
func LoadOrCreateKey(fromEnv, keyFile string) (key []byte, generated bool, err error) {
	if s := strings.TrimSpace(fromEnv); s != "" {
		c, err := NewFromString(s)
		if err != nil {
			return nil, false, fmt.Errorf("解析 %s 失败: %w", "PICGO_WEB_SECRET_KEY", err)
		}
		return c.key, false, nil
	}

	if keyFile == "" {
		return nil, false, ErrKeyMissing
	}

	raw, readErr := os.ReadFile(keyFile)
	if readErr == nil {
		c, err := NewFromString(string(raw))
		if err != nil {
			return nil, false, fmt.Errorf("读取主密钥文件 %s 失败: %w", keyFile, err)
		}
		return c.key, false, nil
	}
	if !os.IsNotExist(readErr) {
		return nil, false, fmt.Errorf("读取主密钥文件 %s 失败: %w", keyFile, readErr)
	}

	// 生成新密钥
	buf := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, false, fmt.Errorf("生成主密钥失败: %w", err)
	}
	if dir := filepath.Dir(keyFile); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, false, fmt.Errorf("创建主密钥目录失败: %w", err)
		}
	}
	// base64 存储，便于人工查看与迁移
	encoded := base64.StdEncoding.EncodeToString(buf)
	if err := os.WriteFile(keyFile, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, false, fmt.Errorf("写入主密钥文件 %s 失败: %w", keyFile, err)
	}
	return buf, true, nil
}

// Encrypt 加密明文，返回 base64 密文。
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	gcm, err := c.aead()
	if err != nil {
		return "", err
	}

	nonce := c.nonce
	if nonce == nil {
		nonce = make([]byte, nonceSize)
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return "", fmt.Errorf("生成 nonce 失败: %w", err)
		}
	}

	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, versionV1)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)

	return base64.StdEncoding.EncodeToString(out), nil
}

// EncryptString 加密字符串。
func (c *Cipher) EncryptString(plaintext string) (string, error) {
	return c.Encrypt([]byte(plaintext))
}

// Decrypt 解密 base64 密文。
func (c *Cipher) Decrypt(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, ErrCipherInvalid
	}
	if len(raw) < 1+nonceSize {
		return nil, ErrCipherInvalid
	}

	version := raw[0]
	if version != versionV1 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}

	gcm, err := c.aead()
	if err != nil {
		return nil, err
	}

	nonce := raw[1 : 1+nonceSize]
	ciphertext := raw[1+nonceSize:]

	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// DecryptString 解密为字符串。
func (c *Cipher) DecryptString(encoded string) (string, error) {
	b, err := c.Decrypt(encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Mask 是 API 响应中敏感字段的统一掩码（D19）。
const Mask = "******"

// IsMasked 判断值是否为掩码（用于「未修改则不提交」的判断，D96/D95）。
func IsMasked(v string) bool {
	return strings.TrimSpace(v) == Mask
}

func (c *Cipher) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	return gcm, nil
}

// ---- 十六进制助手（避免引入 encoding/hex 的额外分支） ----

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, ErrKeyInvalid
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, ok := hexVal(s[i*2])
		if !ok {
			return nil, ErrKeyInvalid
		}
		lo, ok := hexVal(s[i*2+1])
		if !ok {
			return nil, ErrKeyInvalid
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
