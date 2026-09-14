// Package auth 提供鉴权与令牌的**密码学原语**：密码哈希、JWT、随机令牌、
// Cookie、OAuth state 签名、首启引导。
//
// 本包只做「怎么算」，不做业务规则（业务规则在 internal/service）；
// 只依赖 crypto/id/model/config/repository/logger，**不依赖 gin**，
// 因此可被 middleware 与 service 同时复用。
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost 是密码哈希的代价因子（D24：cost = 12）。
const BcryptCost = 12

// dummyHash 用于「账号不存在」时的等时比较（防账号枚举，见 D23 与 API.md §1）。
//
// 若不存在邮箱直接返回错误，攻击者可据响应时间区分「账号不存在」与「密码错误」。
// 因此对不存在的账号也要跑一次同等代价的 bcrypt 比较。
var dummyHash = mustDummyHash()

func mustDummyHash() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("picgo-web-dummy-password-for-timing"), BcryptCost)
	if err != nil {
		// 理论上不会发生；退化为一个固定的合法 bcrypt 串。
		return []byte("$2a$12$C6UzMDM.H6dfI/f/IKcEe.4nQ9Q5jVd7Xq0Y1nS9Zy8Z8Z8Z8Z8Z8")
	}
	return h
}

// HashPassword 生成 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("生成密码哈希失败: %w", err)
	}
	return string(h), nil
}

// VerifyPassword 校验明文是否匹配哈希。
//
// 哈希为空（纯 OAuth 用户）时返回 false —— 调用方应配合 SpendDummyCompare
// 以保证耗时一致。
func VerifyPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// SpendDummyCompare 消耗一次与真实校验等价的 bcrypt 时间。
//
// 用于两条路径：① 邮箱不存在；② 账号是纯 OAuth 用户没有密码。
func SpendDummyCompare(plain string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
}

// RandomString 生成 n 字节随机数据的 base64url 编码（无填充）。
func RandomString(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// passwordAlphabet 去掉了易混淆字符（0/O、1/l/I），便于管理员手抄初始密码。
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GenerateRandomPassword 生成 n 个字符的随机密码（用于首启引导与管理员重置）。
func GenerateRandomPassword(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("密码长度必须为正")
	}
	out := make([]byte, n)
	// 拒绝采样，避免取模引入偏差
	max := byte(256 - (256 % len(passwordAlphabet)))
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("生成随机密码失败: %w", err)
		}
		if buf[0] >= max {
			continue
		}
		out[i] = passwordAlphabet[int(buf[0])%len(passwordAlphabet)]
		i++
	}
	return string(out), nil
}
