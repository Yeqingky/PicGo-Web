// Package id 生成对外标识。
//
// 设计（D77.1）：
//   - **对外一律用 UID**（带前缀的 ULID 字符串），不暴露自增 ID
//   - 自增 ID 仅用于外键与索引（内部）
//
// 前缀便于人工辨识与日志排查：
//
//	up_    Uploads
//	st_    StorageConfigs
//	al_    Albums
//	job_   Jobs
//	usr_   Users
//	tok_   APITokens
//	rt_    RefreshTokens
//	oid_   OAuthIdentities
//	log_   OperationLogs
//	mail_  EmailLogs
//	thm_   Themes（仅用于日志关联，主题本身以 ID 字符串标识）
package id

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// 前缀常量。
const (
	PrefixUpload       = "up_"
	PrefixStorage      = "st_"
	PrefixAlbum        = "al_"
	PrefixJob          = "job_"
	PrefixUser         = "usr_"
	PrefixAPIToken     = "apt_"
	PrefixRefreshToken = "rt_"
	PrefixOAuth        = "oid_"
	PrefixLog          = "log_"
	PrefixEmail        = "log_mail_"
	PrefixTheme        = "thm_"
)

// entropiedReader 是并发安全的 ULID 熵源。
// ulid.Monotonic 保证同一毫秒内生成的 ID 严格递增（便于按时间排序）。
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// New 生成一个不带前缀的 ULID（26 字符，Crockford Base32，大写）。
func New() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// WithPrefix 生成带前缀的对外标识，如 "up_01J8XQ..."。
func WithPrefix(prefix string) string {
	return prefix + New()
}

// Upload 生成图片 UID。
func Upload() string { return WithPrefix(PrefixUpload) }

// Storage 生成存储配置 UID。
func Storage() string { return WithPrefix(PrefixStorage) }

// Album 生成相册 UID。
func Album() string { return WithPrefix(PrefixAlbum) }

// Job 生成任务 UID。
func Job() string { return WithPrefix(PrefixJob) }

// User 生成用户 UID。
func User() string { return WithPrefix(PrefixUser) }

// APIToken 生成 API Token 的 UID（不含明文令牌本身）。
func APIToken() string { return WithPrefix(PrefixAPIToken) }

// RefreshToken 生成 refresh token 的 UID（不含明文令牌本身）。
func RefreshToken() string { return WithPrefix(PrefixRefreshToken) }

// OAuthIdentity 生成第三方身份绑定的 UID。
func OAuthIdentity() string { return WithPrefix(PrefixOAuth) }

// Log 生成操作日志 UID。
func Log() string { return WithPrefix(PrefixLog) }

// EmailLog 生成邮件日志 UID。
func EmailLog() string { return WithPrefix(PrefixEmail) }

// Theme 生成与主题相关的关联 UID（主题自身以 manifest 的 ID 标识）。
func Theme() string { return WithPrefix(PrefixTheme) }

// Valid 判断字符串是否为合法的 ULID（用于校验外部传入的 UID）。
func Valid(s string) bool {
	_, err := ulid.Parse(s)
	return err == nil
}
