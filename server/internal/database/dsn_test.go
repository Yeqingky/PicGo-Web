package database

import (
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
)

func TestDSNSqlite(t *testing.T) {
	cfg := &config.Config{
		DBDriver:   config.DBDriverSQLite,
		SQLitePath: "/var/lib/picgo-web/picgo-web.db",
	}

	dsn, err := DSN(cfg)
	if err != nil {
		t.Fatalf("DSN 返回错误: %v", err)
	}

	if !strings.HasPrefix(dsn, "file:///var/lib/picgo-web/picgo-web.db?") {
		t.Errorf("sqlite DSN 前缀不对: %s", dsn)
	}
	// 三条 PRAGMA 必须都在（WAL / busy_timeout / foreign_keys），外加 synchronous
	for _, want := range []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("sqlite DSN 缺少 %s：%s", want, dsn)
		}
	}
}

func TestDSNSqliteRelativePath(t *testing.T) {
	cfg := &config.Config{
		DBDriver:   config.DBDriverSQLite,
		SQLitePath: "./data/picgo-web.db",
	}
	dsn, err := DSN(cfg)
	if err != nil {
		t.Fatalf("DSN 返回错误: %v", err)
	}
	if !strings.Contains(dsn, "picgo-web.db?") {
		t.Errorf("相对路径未被正确保留: %s", dsn)
	}
}

func TestDSNPostgres(t *testing.T) {
	cfg := &config.Config{
		DBDriver:   config.DBDriverPostgres,
		PGHost:     "db.internal",
		PGPort:     5433,
		PGUser:     "picgo",
		PGPassword: "p@ss word",
		PGDBName:   "picgo_web",
		PGSSLMode:  "require",
		PGTimezone: "Asia/Shanghai",
	}

	dsn, err := DSN(cfg)
	if err != nil {
		t.Fatalf("DSN 返回错误: %v", err)
	}

	for _, want := range []string{
		"host=db.internal",
		"port=5433",
		"user=picgo",
		"dbname=picgo_web",
		"sslmode=require",
		"TimeZone=Asia/Shanghai",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("postgres DSN 缺少 %s：%s", want, dsn)
		}
	}
	// 含空格的密码必须被引号包裹
	if !strings.Contains(dsn, "password='p@ss word'") {
		t.Errorf("含空格密码未被引号包裹：%s", dsn)
	}
}

func TestDSNPostgresQuoteEscape(t *testing.T) {
	cfg := &config.Config{
		DBDriver:   config.DBDriverPostgres,
		PGHost:     "127.0.0.1",
		PGPort:     5432,
		PGUser:     "u",
		PGPassword: `it's a\secret`,
		PGDBName:   "db",
		PGSSLMode:  "disable",
		PGTimezone: "UTC",
	}
	dsn, err := DSN(cfg)
	if err != nil {
		t.Fatalf("DSN 返回错误: %v", err)
	}
	// 单引号与反斜杠都要转义
	if !strings.Contains(dsn, `password='it\'s a\\secret'`) {
		t.Errorf("密码转义不正确：%s", dsn)
	}
}

func TestDSNExplicitOverride(t *testing.T) {
	// cfg.DBDSN 非空时必须完全覆盖拼装结果
	cfg := &config.Config{
		DBDriver:   config.DBDriverPostgres,
		DBDSN:      "postgres://u:p@host:5432/db?sslmode=disable",
		PGHost:     "ignored",
		PGUser:     "ignored",
		PGDBName:   "ignored",
		PGSSLMode:  "disable",
		PGTimezone: "UTC",
	}
	dsn, err := DSN(cfg)
	if err != nil {
		t.Fatalf("DSN 返回错误: %v", err)
	}
	if dsn != cfg.DBDSN {
		t.Errorf("DBDSN 未被原样采用：%s", dsn)
	}
	if strings.Contains(dsn, "ignored") {
		t.Errorf("DBDSN 非空时不应使用拼装字段：%s", dsn)
	}
}

func TestDSNUnsupportedDriver(t *testing.T) {
	cfg := &config.Config{DBDriver: "mysql"}
	if _, err := DSN(cfg); err == nil {
		t.Fatal("不支持的驱动应当返回错误")
	}
}

func TestMaskedDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres kv",
			in:   "host=h port=5432 user=u password=secret dbname=d",
			want: "host=h port=5432 user=u password=****** dbname=d",
		},
		{
			name: "url",
			in:   "postgres://u:secret@h:5432/d",
			want: "postgres://u:******@h:5432/d",
		},
		{
			name: "sqlite 无密码",
			in:   "file:///tmp/x.db?_pragma=journal_mode(WAL)",
			want: "file:///tmp/x.db?_pragma=journal_mode(WAL)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskedDSN(tc.in)
			if got != tc.want {
				t.Errorf("MaskedDSN(%q)\n got = %q\nwant = %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "secret") {
				t.Errorf("掩码后仍含密码明文: %s", got)
			}
		})
	}
}
