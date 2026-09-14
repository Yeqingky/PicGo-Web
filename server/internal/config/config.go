// Package config 负责「启动引导类配置」的加载。
//
// 配置三级分层（DECISIONS.md D18）：
//
//	第 1 层  环境变量 / .env        —— 本包负责，只读、不可热更
//	第 2 层  数据库 Settings 表      —— 由 internal/settings 负责，DB 为真相源
//	第 3 层  代码默认值              —— 见 defaults.go
//
// 本包只处理第 1 层：监听地址、数据库连接、数据目录、加密主密钥、
// agent 地址与令牌、日志级别。业务配置一律走数据库（settings 包）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// DBDriver 数据库方言。
type DBDriver string

const (
	DBDriverSQLite   DBDriver = "sqlite"
	DBDriverPostgres DBDriver = "postgres"
)

// 环境变量名（D81.3 第 2 条：环境变量保持 UPPER_SNAKE_CASE，不受 PascalCase 影响）。
const (
	EnvListen      = "PICGO_WEB_LISTEN"
	EnvDataDir     = "PICGO_WEB_DATA_DIR"
	EnvSecretKey   = "PICGO_WEB_SECRET_KEY"
	EnvLogLevel    = "PICGO_WEB_LOG_LEVEL"
	EnvTrustProxy  = "PICGO_WEB_TRUST_PROXY"
	EnvAgentURL    = "PICGO_WEB_AGENT_URL"
	EnvAgentToken  = "PICGO_WEB_AGENT_TOKEN"
	EnvAgentAuto   = "PICGO_WEB_AGENT_AUTOSTART"
	EnvAgentMock   = "PICGO_WEB_AGENT_MOCK"
	EnvAgentDir    = "PICGO_WEB_AGENT_DIR"
	EnvAgentCmd    = "PICGO_WEB_AGENT_COMMAND"
	EnvAgentNpmReg = "PICGO_WEB_AGENT_NPM_REGISTRY"
	EnvAgentNpmPxy = "PICGO_WEB_AGENT_NPM_PROXY"
	EnvAgentUpPxy  = "PICGO_WEB_AGENT_UPLOAD_PROXY"
	EnvAllowPriv   = "PICGO_WEB_ALLOW_PRIVATE_FETCH"
	EnvDevMode     = "PICGO_WEB_DEV"
	EnvStaticDir   = "PICGO_WEB_STATIC_DIR"
	EnvThemesDir   = "PICGO_WEB_THEMES_DIR"
	EnvThemeSeed   = "PICGO_WEB_THEME_SEED"

	EnvDBDriver = "PICGO_WEB_DB_DRIVER"
	EnvDBDSN    = "PICGO_WEB_DB_DSN"
	EnvDBAuto   = "PICGO_WEB_DB_AUTO_MIGRATE"

	EnvSQLitePath = "PICGO_WEB_SQLITE_PATH"

	EnvPGHost     = "PICGO_WEB_PG_HOST"
	EnvPGPort     = "PICGO_WEB_PG_PORT"
	EnvPGUser     = "PICGO_WEB_PG_USER"
	EnvPGPassword = "PICGO_WEB_PG_PASSWORD"
	EnvPGDBName   = "PICGO_WEB_PG_DBNAME"
	EnvPGSSLMode  = "PICGO_WEB_PG_SSLMODE"
	EnvPGTimezone = "PICGO_WEB_PG_TIMEZONE"

	EnvDBMaxOpenConns    = "PICGO_WEB_DB_MAX_OPEN_CONNS"
	EnvDBMaxIdleConns    = "PICGO_WEB_DB_MAX_IDLE_CONNS"
	EnvDBConnMaxLifetime = "PICGO_WEB_DB_CONN_MAX_LIFETIME"
)

// DefaultListen 等默认值是「进程能否启动」级别的兜底，
// 与业务默认值（defaults.go）分开：这些不进数据库。
const (
	DefaultListen      = "0.0.0.0:8080"
	DefaultDataDir     = "./data"
	DefaultLogLevel    = "info"
	DefaultAgentURL    = "http://127.0.0.1:36678"
	DefaultAgentToken  = ""
	DefaultSQLitePath  = "picgo-web.db"
	DefaultPGHost      = "127.0.0.1"
	DefaultPGPort      = 5432
	DefaultPGSSLMode   = "disable"
	DefaultPGTimezone  = "Asia/Shanghai"
	DefaultMaxOpenConn = 25
	DefaultMaxIdleConn = 5
	DefaultConnLife    = time.Hour
)

// Config 是启动引导配置。字段名用 PascalCase（D81），
// 但每个字段的来源环境变量保持 UPPER_SNAKE_CASE。
type Config struct {
	// 服务
	Listen     string
	DataDir    string
	LogLevel   string
	TrustProxy bool
	DevMode    bool

	// 静态资源（Go 内置 SPA 的产物目录；生产环境用 embed）
	StaticDir string

	// 主题
	ThemesDir     string // 主题根目录；为空则取 <DataDir>/themes
	ThemeSeedFrom string // seed 源目录；为空则用内嵌默认主题

	// 安全
	SecretKey string // 为空则从 <DataDir>/secret.key 读取或生成

	// 数据库
	DBDriver          DBDriver
	DBDSN             string // 非空时完全覆盖下面的拼装结果
	DBAutoMigrate     bool
	SQLitePath        string
	PGHost            string
	PGPort            int
	PGUser            string
	PGPassword        string
	PGDBName          string
	PGSSLMode         string
	PGTimezone        string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// picgo-agent 侧车
	AgentURL       string
	AgentToken     string
	AgentAutostart bool
	AgentMock      bool
	// AgentDir picgo-agent 所在目录（含 dist/index.js）。留空则自动探测。
	AgentDir string
	// AgentCommand 显式指定启动命令（空格分隔）。优先级高于 AgentDir。
	AgentCommand string
	// 传给 agent 的 npm 配置与上传代理（agent 侧用于插件安装与上传）。
	AgentNpmRegistry string
	AgentNpmProxy    string
	AgentUploadProxy string

	// 抓取外部 URL 时是否允许私有网段（SSRF 防护开关）
	AllowPrivateFetch bool
}

// Load 读取 .env（如果存在）后从环境变量构建配置。
//
// envFiles 为可选的 .env 路径；不传时按顺序尝试 "./.env"、".env.local"。
// 已存在的环境变量优先于 .env（godotenv.Load 不覆盖已有变量）。
func Load(envFiles ...string) (*Config, error) {
	if len(envFiles) == 0 {
		envFiles = []string{".env", ".env.local"}
	}
	for _, f := range envFiles {
		if _, err := os.Stat(f); err == nil {
			if err := godotenv.Load(f); err != nil {
				return nil, fmt.Errorf("加载 %s 失败: %w", f, err)
			}
		}
	}

	cfg := &Config{
		Listen:            envStr(EnvListen, DefaultListen),
		DataDir:           envStr(EnvDataDir, DefaultDataDir),
		LogLevel:          envStr(EnvLogLevel, DefaultLogLevel),
		TrustProxy:        envBool(EnvTrustProxy, false),
		DevMode:           envBool(EnvDevMode, false),
		StaticDir:         envStr(EnvStaticDir, ""),
		ThemeSeedFrom:     envStr(EnvThemeSeed, ""),
		SecretKey:         envStr(EnvSecretKey, ""),
		DBDriver:          DBDriver(strings.ToLower(envStr(EnvDBDriver, string(DBDriverSQLite)))),
		DBDSN:             envStr(EnvDBDSN, ""),
		DBAutoMigrate:     envBool(EnvDBAuto, true),
		SQLitePath:        envStr(EnvSQLitePath, ""),
		PGHost:            envStr(EnvPGHost, DefaultPGHost),
		PGPort:            envInt(EnvPGPort, DefaultPGPort),
		PGUser:            envStr(EnvPGUser, ""),
		PGPassword:        envStr(EnvPGPassword, ""),
		PGDBName:          envStr(EnvPGDBName, ""),
		PGSSLMode:         envStr(EnvPGSSLMode, DefaultPGSSLMode),
		PGTimezone:        envStr(EnvPGTimezone, DefaultPGTimezone),
		DBMaxOpenConns:    envInt(EnvDBMaxOpenConns, DefaultMaxOpenConn),
		DBMaxIdleConns:    envInt(EnvDBMaxIdleConns, DefaultMaxIdleConn),
		DBConnMaxLifetime: envDuration(EnvDBConnMaxLifetime, DefaultConnLife),
		AgentURL:          strings.TrimRight(envStr(EnvAgentURL, DefaultAgentURL), "/"),
		AgentToken:        envStr(EnvAgentToken, DefaultAgentToken),
		AgentAutostart:    envBool(EnvAgentAuto, true),
		AgentMock:         envBool(EnvAgentMock, false),
		AgentDir:          envStr(EnvAgentDir, ""),
		AgentCommand:      envStr(EnvAgentCmd, ""),
		AgentNpmRegistry:  envStr(EnvAgentNpmReg, ""),
		AgentNpmProxy:     envStr(EnvAgentNpmPxy, ""),
		AgentUploadProxy:  envStr(EnvAgentUpPxy, ""),
		AllowPrivateFetch: envBool(EnvAllowPriv, false),
	}

	cfg.ThemesDir = envStr(EnvThemesDir, "")
	if cfg.ThemesDir == "" {
		cfg.ThemesDir = filepath.Join(cfg.DataDir, "themes")
	}

	switch cfg.DBDriver {
	case DBDriverSQLite:
		if cfg.SQLitePath == "" {
			cfg.SQLitePath = filepath.Join(cfg.DataDir, DefaultSQLitePath)
		}
	case DBDriverPostgres:
		// 端口/主机等已带默认值，必填项在 DSN 阶段校验
	default:
		return nil, fmt.Errorf("不支持的 %s: %q（只支持 sqlite / postgres）", EnvDBDriver, cfg.DBDriver)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 校验必填项。**只校验「缺了就无法启动」的项**，
// 业务配置的校验在 settings 包。
func (c *Config) Validate() error {
	if c.DBDriver == DBDriverPostgres && c.DBDSN == "" {
		switch {
		case c.PGUser == "":
			return fmt.Errorf("%s 或 %s 必填", EnvDBDSN, EnvPGUser)
		case c.PGDBName == "":
			return fmt.Errorf("%s 或 %s 必填", EnvDBDSN, EnvPGDBName)
		}
	}
	if c.DBMaxOpenConns < 1 {
		return fmt.Errorf("%s 必须 >= 1", EnvDBMaxOpenConns)
	}
	if c.DBMaxIdleConns < 0 {
		return fmt.Errorf("%s 必须 >= 0", EnvDBMaxIdleConns)
	}
	if c.DBMaxIdleConns > c.DBMaxOpenConns {
		return fmt.Errorf("%s 不能大于 %s", EnvDBMaxIdleConns, EnvDBMaxOpenConns)
	}
	return nil
}

// SecretKeyFile 返回主密钥文件的路径（<DataDir>/secret.key）。
// 见 D19：主密钥绝不进数据库。
func (c *Config) SecretKeyFile() string {
	return filepath.Join(c.DataDir, "secret.key")
}

// AgentTokenFile 返回 agent 令牌文件的路径。
func (c *Config) AgentTokenFile() string {
	return filepath.Join(c.DataDir, "agent-token.txt")
}

// InitialAdminPasswordFile 首启管理员随机密码的落盘位置（0600，D32）。
func (c *Config) InitialAdminPasswordFile() string {
	return filepath.Join(c.DataDir, "initial-admin-password.txt")
}

// UploadsDir 上传暂存目录。
func (c *Config) UploadsDir() string {
	return filepath.Join(c.DataDir, "uploads")
}

// PicgoConfigDir picgo 的 baseDir（config.json 与 node_modules 所在）。
func (c *Config) PicgoConfigDir() string {
	return filepath.Join(c.DataDir, "picgo")
}

// PicgoConfigPath picgo 的 config.json 路径。
func (c *Config) PicgoConfigPath() string {
	return filepath.Join(c.PicgoConfigDir(), "config.json")
}

// ThumbsDir 预留：本项目不做缩略图（D84），此目录不创建、不写入。
//
// Deprecated: 保留仅为文档一致性；实现中不得使用。
func (c *Config) ThumbsDir() string { return filepath.Join(c.DataDir, "thumbs") }

// EnsureDirs 创建所有运行期需要的目录。
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.DataDir,
		c.UploadsDir(),
		c.PicgoConfigDir(),
		c.ThemesDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return nil
}

// ---- env 读取助手 ----

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off", "n":
		return false
	default:
		return def
	}
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return d
}
