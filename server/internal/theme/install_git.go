package theme

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// GitInstallRequest 是一次「从 Git 安装/更新主题」的请求（D100）。
type GitInstallRequest struct {
	// URL 是 **https** Git 仓库地址（仅 https，见 ValidateGitURL）。
	URL string
	// Overwrite 允许覆盖同名主题（与 zip 安装语义一致）。
	Overwrite bool
}

// InstallFromGit 从 https Git 仓库安装主题（D100）。
//
// 流程：校验 URL → 浅克隆到临时目录 → **与 zip 安装同一套清单校验**
// （manifest 七项 + Pages；文件数/体积限额按 zip 语义套用）→ 原子替换。
// 任何一步失败都不落盘；审计与 zip 安装走同一个 `theme.install` 类型，
// Detail 里带 `Via=git`、`URL`、`Branch` 便于区分来源。
//
// 这是**同步**调用（handler 内联等待）：浅克隆小仓库通常几秒；
// git 自身有 2 分钟超时兜底（gitCloneTimeout），不会无限挂起。
func (s *Service) InstallFromGit(ctx context.Context, req GitInstallRequest) (*GitInstallResult, error) {
	res, err := s.installFromGit(ctx, req)
	if err != nil {
		s.auditor.Log(ctx, AuditEntry{
			Type:       model.LogTypeThemeInstall,
			Status:     model.LogStatusFailed,
			TargetType: "theme",
			Detail:     map[string]any{"Via": "git", "URL": req.URL, "Overwrite": req.Overwrite},
			Cause:      err,
		})
		return nil, err
	}

	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeInstall,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		TargetUID:  res.ID,
		Detail: map[string]any{
			"Via": "git", "URL": req.URL, "Branch": res.Branch,
			"Name": res.Name, "Version": res.Version, "Overwrite": req.Overwrite,
		},
	})
	return res, nil
}

// InstallResult 的 Git 扩展字段通过独立结果类型承载，避免污染 zip 语义。
// GitInstallResult 在 InstallResult 之外附加来源信息。
type GitInstallResult struct {
	InstallResult
	// Branch 实际克隆到的分支（通常为仓库默认分支）。
	Branch string
}

func (s *Service) installFromGit(ctx context.Context, req GitInstallRequest) (*GitInstallResult, error) {
	if err := ValidateGitURL(req.URL); err != nil {
		return nil, err
	}
	limits := readLimits(s.settings)

	if err := os.MkdirAll(s.store.ThemesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("创建主题目录失败: %w", err)
	}
	tmpBase, err := os.MkdirTemp(s.store.ThemesDir(), ".tmp-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	// 只要还没成功 rename，就确保临时目录被清掉（原子性 / 不留半个主题）
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmpBase)
		}
	}()

	cloneDir := filepath.Join(tmpBase, "repo")
	branch, err := cloneRepo(ctx, req.URL, cloneDir)
	if err != nil {
		return nil, err
	}

	res, err := s.installFromDir(cloneDir, limits, req.Overwrite)
	if err != nil {
		return nil, err
	}
	keep = true // cloneDir 已被 rename 走
	_ = os.RemoveAll(tmpBase)

	s.log.Info("主题从 Git 安装成功",
		"id", res.ID, "version", res.Version, "url", req.URL, "branch", branch,
	)
	return &GitInstallResult{InstallResult: *res, Branch: branch}, nil
}

// installFromDir 把一个**本机目录**（Git 克隆结果 / 未来的其他源）按主题规范
// 校验后安装。与 zip 的 install() 共享同一套语义：
//
//   - manifest 必须在目录根（Git 仓库没有 zip 的「唯一顶层目录」形态）
//   - 清单七项校验 + index.html 存在
//   - 文件数 / 总体积 / 单文件体积限额（跳过 .git）
//   - 目标冲突检查（default 允许覆盖但需显式 Overwrite）+ 原子替换
func (s *Service) installFromDir(srcDir string, limits Limits, overwrite bool) (*InstallResult, error) {
	// ---- 1. 统计文件数与总体积（跳过 .git）----
	var totalFiles int
	var totalBytes int64
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && path == filepath.Join(srcDir, ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: %q 不是常规文件", ErrArchiveInvalid, filepath.Base(path))
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("%w: %q 为 %d 字节（上限 %d）", ErrSingleFileTooLarge, path, info.Size(), limits.MaxFileBytes)
		}
		totalFiles++
		totalBytes += info.Size()
		if totalFiles > limits.MaxFiles {
			return fmt.Errorf("%w: %d > %d", ErrTooManyFiles, totalFiles, limits.MaxFiles)
		}
		if totalBytes > limits.MaxExtractBytes {
			return fmt.Errorf("%w: %d > %d 字节", ErrExtractTooLarge, totalBytes, limits.MaxExtractBytes)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchiveInvalid, walkErr)
	}

	// ---- 2. 读根目录 manifest ----
	manifestPath := filepath.Join(srcDir, ManifestFile)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 仓库根缺少 %s（主题必须在仓库根目录）", ErrArchiveInvalid, ManifestFile)
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", ManifestFile, err)
	}
	manifest, err := ParseManifest(raw, limits.MaxManifestBytes)
	if err != nil {
		return nil, err
	}

	// ---- 3. 清单校验（含 index.html 存在性）----
	_, indexErr := os.Stat(filepath.Join(srcDir, IndexFile))
	hasIndex := indexErr == nil
	if err := manifest.Validate(manifest.ID, hasIndex); err != nil {
		return nil, err
	}

	targetDir := s.store.DirOf(manifest.ID)

	// ---- 4. 冲突检查（与 zip 一致：default 可覆盖但需显式 Overwrite）----
	if dirExists(targetDir) && !overwrite {
		return nil, fmt.Errorf("%w: %s", ErrThemeExists, manifest.ID)
	}

	// ---- 5. 复制到临时目录（跳过 .git）----
	if err := os.MkdirAll(s.store.ThemesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("创建主题目录失败: %w", err)
	}
	tmpDir, err := os.MkdirTemp(s.store.ThemesDir(), ".tmp-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	if err := copyDirContents(srcDir, tmpDir); err != nil {
		return nil, err
	}

	// ---- 6. 原子替换 ----
	if err := s.atomicReplace(tmpDir, targetDir); err != nil {
		return nil, err
	}
	keepTmp = true

	s.log.Info("主题目录安装成功",
		"id", manifest.ID, "version", manifest.Version,
		"files", totalFiles, "bytes", totalBytes, "overwrite", overwrite,
	)
	return &InstallResult{
		ID: manifest.ID, Name: manifest.Name.Resolve("zh-CN"), Version: manifest.Version,
	}, nil
}

// DefaultThemeGitURL 是默认主题的官方仓库（settings 未配置时的兜底）。
const DefaultThemeGitURL = "https://github.com/Yeqingky/PicGo-Web-Theme.git"

// seedFromGit 尝试从 Git 拉取默认主题到 dst（D100：默认主题经 Git 通道导入）。
//
// 失败**不致命**（返回 err 由调用方决定是否回退内嵌副本）——
// 离线部署 / GitHub 不可达 / git 二进制缺失都属于正常环境差异。
func seedFromGit(gitURL, dst string) error {
	if err := ValidateGitURL(gitURL); err != nil {
		return err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("%w: 环境内没有 git 可执行文件", ErrGitClone)
	}

	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".tmp-git-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if _, err := cloneRepo(nil, gitURL, filepath.Join(tmp, "repo")); err != nil {
		return err
	}
	return copyDirContents(filepath.Join(tmp, "repo"), dst)
}
