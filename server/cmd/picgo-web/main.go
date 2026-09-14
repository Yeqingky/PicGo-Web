// Command picgo-web 是 PicGo-Web 的主程序。
//
// 装配顺序：
//
//	config → logger → data dirs → 主密钥 → database → migrations → settings → HTTP server
//
// 启动失败一律以非零退出码终止，并把原因写日志（便于容器编排定位）。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/server"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

func main() {
	if err := run(); err != nil {
		// 此时 slog 可能还没初始化，直接写 stderr 保证可见
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ---- 1. 配置 ----
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// ---- 2. 日志 ----
	log := logger.New(cfg.LogLevel, cfg.DevMode)
	if cfg.DevMode {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	log.Info("PicGo-Web 启动中",
		"version", server.Version,
		"listen", cfg.Listen,
		"data_dir", cfg.DataDir,
		"db_driver", string(cfg.DBDriver),
		"dev", cfg.DevMode,
	)

	// ---- 3. 目录 ----
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// ---- 4. 主密钥（绝不进数据库，D19） ----
	key, generated, err := crypto.LoadOrCreateKey(cfg.SecretKey, cfg.SecretKeyFile())
	if err != nil {
		return fmt.Errorf("准备加密主密钥失败: %w", err)
	}
	cipher, err := crypto.New(key)
	if err != nil {
		return fmt.Errorf("初始化加密器失败: %w", err)
	}
	if generated {
		log.Warn("已生成新的加密主密钥，请立即备份（丢失后已加密的配置将无法解密）",
			"path", cfg.SecretKeyFile(),
		)
	}

	// ---- 5. 数据库 ----
	db, err := database.Open(cfg, log)
	if err != nil {
		return err
	}
	log.Info("数据库已连接", "driver", string(db.Driver))

	// ---- 6. 迁移 ----
	if cfg.DBAutoMigrate {
		if err := database.Migrate(db, log); err != nil {
			return fmt.Errorf("数据库迁移失败: %w", err)
		}
	} else {
		version, err := database.CurrentVersion(db.DB)
		if err != nil {
			return fmt.Errorf("读取数据库版本失败: %w", err)
		}
		if version < database.SchemaVersion {
			log.Warn("已跳过自动迁移，但数据库版本落后",
				"current", version, "expected", database.SchemaVersion,
			)
		}
	}

	// ---- 7. 首启引导：表里没有用户时创建一个初始管理员（D32）----
	//
	// 必须在 HTTP 服务启动前完成，否则系统里没有任何账号可登录。
	// 内部幂等：已有用户时直接返回（重复启动不会重建）。
	userRepo := repository.NewUserRepo(db.DB)
	if err := auth.Bootstrap(userRepo, cfg, log); err != nil {
		return fmt.Errorf("首启引导失败: %w", err)
	}

	// ---- 8. 配置服务 ----
	settingRepo := repository.NewSettingRepo(db.DB)
	settingsSvc, err := settings.New(settingRepo, cipher, log)
	if err != nil {
		return fmt.Errorf("初始化配置服务失败: %w", err)
	}
	log.Debug("配置服务已就绪", "keys", len(settingsSvc.All()))

	// 配置变更回调（后续 W4/W5 会在此处推送 agent、刷新限流器）
	settingsSvc.OnChanged(func(ev settings.ChangedEvent) {
		log.Debug("配置项已更新",
			"key", ev.Key, "source", string(ev.Source), "by", ev.By,
		)
	})

	// ---- 9. HTTP 服务 ----
	app, err := server.New(server.Deps{
		Cfg:        cfg,
		Log:        log,
		DB:         db,
		Settings:   settingsSvc,
		SigningKey: key,
	})
	if err != nil {
		return fmt.Errorf("装配 HTTP 服务失败: %w", err)
	}

	shutdownGrace := time.Duration(settingsSvc.GetInt("upload.shutdownGraceSeconds", 30)) * time.Second
	return app.Run(context.Background(), shutdownGrace)
}
