package database

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
)

// SQLite PRAGMA。
//
//   - journal_mode(WAL)   并发读写（SQLite 默认的 rollback journal 会导致写阻塞读）
//   - busy_timeout(5000)  写锁等待 5s 而不是立刻 database is locked
//   - foreign_keys(1)     我们**不建** FOREIGN KEY 约束（D77），但保持 SQLite 默认行为一致
//   - synchronous(NORMAL) WAL 下的推荐值，兼顾安全与性能
const sqlitePragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"

// DSN 根据驱动拼装连接串。
//
// 规则（docs/DATA-MODEL.md §0.1）：
//   - cfg.DBDSN 非空时**完全覆盖**下面拼装的结果（便于云厂商托管连接串）
//   - sqlite：<path> + PRAGMA
//   - postgres：key=value 形式（pgx 接受）
func DSN(cfg *config.Config) (string, error) {
	if strings.TrimSpace(cfg.DBDSN) != "" {
		return strings.TrimSpace(cfg.DBDSN), nil
	}

	switch cfg.DBDriver {
	case config.DBDriverSQLite:
		path := strings.TrimSpace(cfg.SQLitePath)
		if path == "" {
			return "", fmt.Errorf("SQLite 路径为空")
		}
		// 路径可能含特殊字符（空格、中文），拼接 query 前先编码路徑
		return sqliteDSN(path), nil

	case config.DBDriverPostgres:
		return postgresDSN(cfg), nil

	default:
		return "", fmt.Errorf("不支持的数据库驱动: %q", cfg.DBDriver)
	}
}

// sqliteDSN 拼装 SQLite DSN。
//
// 顺序必须是 [path]?[query]；路径里的 `?` / `#` 需要转义，否则会被当成 query 分隔符。
func sqliteDSN(path string) string {
	// file: URI 形式可安全表达任意路径
	u := &url.URL{Scheme: "file", Opaque: path}
	base := u.String()
	if strings.HasPrefix(path, "/") {
		// 绝对路径：file:///abs/path
		base = "file://" + path
	}
	return base + "?" + sqlitePragmas
}

// postgresDSN 拼装 pgx 的 key=value DSN。
//
// 值里若含空格或单引号，需要用单引号包裹并转义（libpq 规则）。
func postgresDSN(cfg *config.Config) string {
	parts := []struct {
		key string
		val string
	}{
		{"host", cfg.PGHost},
		{"port", fmt.Sprintf("%d", cfg.PGPort)},
		{"user", cfg.PGUser},
		{"password", cfg.PGPassword},
		{"dbname", cfg.PGDBName},
		{"sslmode", cfg.PGSSLMode},
		{"TimeZone", cfg.PGTimezone},
	}

	var b strings.Builder
	for _, p := range parts {
		if p.val == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(quotePGValue(p.val))
	}
	return b.String()
}

// quotePGValue 按 libpq 规则转义值：
// 含空格、单引号或反斜杠时用单引号包裹，并把内部单引号/反斜杠转义。
func quotePGValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range v {
		if r == '\'' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// MaskedDSN 返回可安全打日志的 DSN（隐藏密码）。
func MaskedDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// postgres key=value 形式
	if strings.Contains(dsn, "password=") {
		parts := strings.Fields(dsn)
		for i, p := range parts {
			if strings.HasPrefix(p, "password=") {
				parts[i] = "password=" + maskLiteral
			}
		}
		return strings.Join(parts, " ")
	}
	// URL 形式：**手工拼接**，因为 url.String() 会把 `*` 转义成 %2A
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, hasPwd := u.User.Password(); hasPwd {
			return u.Scheme + "://" + u.User.Username() + ":" + maskLiteral + "@" + u.Host + u.RequestURI()
		}
		return dsn
	}
	return dsn
}

// maskLiteral 是日志里的密码占位（不用 crypto.Mask 以免包间依赖）。
const maskLiteral = "******"
