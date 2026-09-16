package theme

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitCloneTimeout 是单次 git clone 的总时长上限。
//
// GitHub 浅克隆小仓库通常 1~3s；给到 2 分钟已能容忍慢网络，
// 再长不如直接失败（HTTP handler 在等这次同步调用）。
const gitCloneTimeout = 2 * time.Minute

// gitSkipDirNames 是从克隆结果复制到主题目录时**永远跳过**的目录名。
//
// `.git` 必须跳过：主题目录是运行时数据，不应携带版本库（体积、以及
// 避免被静态托管意外暴露）；`.tmp-*` 是本系统自己的安装临时目录。
var gitSkipDirNames = map[string]bool{".git": true}

// ValidateGitURL 校验一个 Git 源地址。
//
// 只允许 **https://** 指向明确的 host（D100）：
//   - 不支持 `file://` / `ssh://` / `git@`：容器内通常没有对应凭据，
//     且本地路径与 zip 安装的攻击面不同，不做
//   - 不允许携带用户信息（`https://user:pass@...`）：本系统不留存凭据，
//     私有仓库请自行部署后内网拉取
func ValidateGitURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("%w: Git 地址为空", ErrGitInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGitInvalid, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: 仅支持 https:// 地址，收到 %q", ErrGitInvalid, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: 缺少主机名", ErrGitInvalid)
	}
	if u.User != nil {
		return fmt.Errorf("%w: 不允许在地址中携带凭据", ErrGitInvalid)
	}
	return nil
}

// cloneRepo 把 Git 仓库浅克隆到 dst（调用方保证 dst 不存在或已清空）。
//
// 安全与健壮性：
//   - 入口先过 ValidateGitURL（对内函数也校验，防绕过）
//   - `--depth 1 --single-branch`：只取默认分支最新快照，主题用不到历史
//   - 子模块**不递归**（git 默认行为）：清单规范没有子模块语义
//   - GIT_TERMINAL_PROMPT=0 + GIT_ASKPASS=echo：凭据缺失时**立即失败**
//     而不是挂住等输入（HTTP handler 在等）
//   - 总时长由 ctx 控制（外层给 gitCloneTimeout）
//
// 返回值里给出实际克隆到的分支（HEAD 解引用），便于日志与审计。
func cloneRepo(ctx context.Context, raw, dst string) (branch string, err error) {
	raw = strings.TrimSpace(raw)
	if err := ValidateGitURL(raw); err != nil {
		return "", err
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), gitCloneTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "git", "clone",
		"--depth", "1", "--single-branch", "--quiet",
		raw, dst,
	)
	// 防交互：任何凭据提示都直接失败；时间戳一致性（reproducible）
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: 克隆超时（%s）", ErrGitClone, gitCloneTimeout)
		}
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return "", fmt.Errorf("%w: %s", ErrGitClone, msg)
	}

	branch, err = headBranch(dst)
	if err != nil {
		return "", err
	}
	return branch, nil
}

// headBranch 读取克隆仓库的当前分支名（日志/审计用，失败不致命）。
func headBranch(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", nil // 不致命：个别仓库可能处于 detached 状态
	}
	return strings.TrimSpace(string(out)), nil
}

// copyDirContents 把 src 目录内容复制到 dst，**跳过 .git 等版本库目录**。
//
// 与 embedded.go 的 copyThemeDir（seed 用、权限位固定）不同：这里为 Git
// 安装服务，保留文件权限原样，但绝不携带 .git（主题目录是运行时数据）。
func copyDirContents(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		// 顶层跳过 .git / 临时目录；主题规范也没有子模块语义
		if d.IsDir() && gitSkipDirNames[d.Name()] && !strings.Contains(rel, string(filepath.Separator)) {
			return filepath.SkipDir
		}

		target, joinErr := safeJoin(dst, rel)
		if joinErr != nil {
			return joinErr
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			// 符号链接等非常规条目：拒绝（与 zip 安装的「拒绝符号链接」一致）
			return fmt.Errorf("%w: %q 不是常规文件", ErrArchiveInvalid, rel)
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > readLimits(nil).MaxFileBytes {
			return fmt.Errorf("%w: %q 超过单文件上限", ErrSingleFileTooLarge, rel)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
