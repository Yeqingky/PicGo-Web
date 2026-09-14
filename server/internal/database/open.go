// Package database 负责数据库连接、方言适配与版本化迁移。
//
// 设计要点（docs/DATA-MODEL.md §0、§9）：
//
//   - 双方言：SQLite（默认，纯 Go / 免 CGO）/ PostgreSQL（pgx，同样纯 Go）
//   - **命名策略 NoLowerCase: true**（D81.4）：否则 `User` 会被折成 `users` 表
//   - **迁移只追加**，已发布条目永不修改（D77）
//   - 每条迁移在**单个事务**内完成，失败回滚
//   - 需要写原生 SQL 时，**标识符必须加双引号**（PgSQL 会把未加引号的折成小写）
package database

import (
	"fmt"
	"log/slog"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
)

// DB 包装 *gorm.DB，附带方言信息。
type DB struct {
	*gorm.DB
	Driver config.DBDriver
}

// Open 按配置打开数据库连接并配置连接池。
func Open(cfg *config.Config, log *slog.Logger) (*DB, error) {
	dsn, err := DSN(cfg)
	if err != nil {
		return nil, err
	}

	gormCfg := &gorm.Config{
		// ⚠️ 关键：不做 snake_case 转换，表名/列名保持 PascalCase（D81.4）
		NamingStrategy: schema.NamingStrategy{
			NoLowerCase: true,
		},
		// 迁移与建表由我们自己的 schemaMigrations 控制，不用 AutoMigrate
		DisableForeignKeyConstraintWhenMigrating: true,
		// 关闭 GORM 默认日志（我们用 slog），只保留错误
		Logger: gormLogger(log),
		// 批量插入分批，避免 SQLite 变量数上限
		CreateBatchSize: 100,
	}

	var dialector gorm.Dialector
	switch cfg.DBDriver {
	case config.DBDriverPostgres:
		dialector = postgres.Open(dsn)
	case config.DBDriverSQLite:
		dialector = sqlite.Open(dsn)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %q", cfg.DBDriver)
	}

	gdb, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败（%s，%s）: %w", cfg.DBDriver, MaskedDSN(dsn), err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层连接失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	// SQLite 是单写多读：把连接数压到 1 会严重限制并发读，
	// 但 WAL + busy_timeout 已能处理写冲突，因此保留配置值。
	if cfg.DBDriver == config.DBDriverSQLite {
		sqlDB.SetMaxOpenConns(1)
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("数据库 Ping 失败: %w", err)
	}

	return &DB{DB: gdb, Driver: cfg.DBDriver}, nil
}

// IsPostgres 便于在迁移与 SQL 分支里判断方言。
func (d *DB) IsPostgres() bool { return d.Driver == config.DBDriverPostgres }

// IsSQLite 便于在迁移与 SQL 分支里判断方言。
func (d *DB) IsSQLite() bool { return d.Driver == config.DBDriverSQLite }

// Quote 按方言给标识符加双引号。
//
// ⚠️ 手写原生 SQL（迁移、索引）必须用它包住**每一个**表名与列名：
// PostgreSQL 会把未加引号的标识符折成小写，`SELECT * FROM Users`
// 在 PgSQL 下会报 `relation "users" does not exist`（D81.4）。
func (d *DB) Quote(ident string) string {
	return `"` + ident + `"`
}

// Table 返回带引号的表名限定形式。
func (d *DB) Table(name string) string { return d.Quote(name) }

// gormLogger 让 GORM 的日志走 slog，并把级别提高到 Warn 以减少噪音。
//
// 开发模式下想看到 SQL 时把 PICGO_WEB_LOG_LEVEL 设为 debug，
// 并把下面的 LogLevel 根据配置切换。
func gormLogger(log *slog.Logger) logger.Interface {
	if log == nil {
		return logger.Default.LogMode(logger.Silent)
	}
	return logger.New(gormSlogWriter{log: log}, logger.Config{
		SlowThreshold:             0,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

type gormSlogWriter struct{ log *slog.Logger }

func (w gormSlogWriter) Printf(format string, args ...any) {
	w.log.Warn(fmt.Sprintf(format, args...), "component", "gorm")
}
