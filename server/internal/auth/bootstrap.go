package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
)

// BootstrapAdminEmail 是首启引导创建的管理员邮箱。
const BootstrapAdminEmail = "admin@localhost"

// BootstrapAdminNickname 是首启引导创建的管理员昵称。
const BootstrapAdminNickname = "admin"

// Bootstrap 在**没有任何用户**时创建初始管理员（D32）。
//
// 行为：
//   - `Users` 表非空 → 直接返回（幂等：重复启动不会重复建号）
//   - 为空 → 创建 admin（Role=admin、MustChangePassword=true）
//     随机密码同时：
//     ① 以 WARN 级别打印到日志
//     ② 写入 <DataDir>/initial-admin-password.txt（权限 0600）
//
// 为什么密码要落盘：日志在容器里可能被日志驱动切走，运维需要一个稳定的读取点。
// 文件权限 0600 保证只有服务进程属主可读。
func Bootstrap(users *repository.UserRepo, cfg *config.Config, log *slog.Logger) error {
	if users == nil {
		return errors.New("bootstrap: users repo 为空")
	}

	n, err := users.Count()
	if err != nil {
		return fmt.Errorf("检查用户数量失败: %w", err)
	}
	if n > 0 {
		return nil
	}

	password, err := GenerateRandomPassword(16)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	now := model.Now()
	admin := &model.User{
		UID:                id.User(),
		Email:              BootstrapAdminEmail,
		PasswordHash:       hash,
		Role:               model.UserRoleAdmin,
		Status:             model.UserStatusActive,
		CapacityBytes:      0, // 管理员不限额（且管理员本就跳过配额校验，D20）
		MustChangePassword: true,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	profile := &model.UserProfile{
		UserUID:   admin.UID,
		Nickname:  BootstrapAdminNickname,
		Locale:    "zh-CN",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := users.CreateWithProfile(admin, profile); err != nil {
		return fmt.Errorf("创建初始管理员失败: %w", err)
	}

	// 落盘（0600）。写入失败不阻断启动 —— 日志里仍有密码。
	if path := cfg.InitialAdminPasswordFile(); path != "" {
		if err := writeSecretFile(path, password); err != nil {
			log.Error("写入初始管理员密码文件失败，请从本行日志中抄录密码",
				"path", path, "err", err)
		} else {
			log.Warn("初始管理员密码已写入文件（首次登录后请立即修改并删除该文件）",
				"path", path)
		}
	}

	// 密码只在此处出现一次，且必须是 WARN 以便在默认日志级别下可见。
	log.Warn("已创建初始管理员账号，请立即登录并修改密码",
		"email", BootstrapAdminEmail,
		"password", password,
		"must_change_password", true,
	)

	return nil
}

// writeSecretFile 以 0600 权限写入敏感文本。
//
// 先写临时文件再原子改名，避免出现「半个文件」被读到；
// 同时在创建时就指定 0600，避免「先创建后 chmod」之间的窗口期泄露。
func writeSecretFile(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, ".secret-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// 改名成功后该文件已不存在，忽略错误即可
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
