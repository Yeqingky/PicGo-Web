package theme

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// maxZipEntriesHardCap 是**任何配置都不能突破**的文件数硬上限。
//
// `theme.maxFiles` 是可配置的，但读取 zip 目录本身就需要遍历 entry；
// 这里加一层硬上限，避免配置被误设成极大值时把内存吃满。
const maxZipEntriesHardCap = 200000

// InstallRequest 是一次 zip 安装请求。
type InstallRequest struct {
	// File 是 zip 数据的随机读取句柄（multipart.File 满足）。
	File io.ReaderAt
	// Size 是压缩包字节数（用于大小校验与 zip.NewReader）。
	Size int64
	// Overwrite 允许覆盖同名主题。
	Overwrite bool
}

// InstallResult 是安装成功的结果。
type InstallResult struct {
	ID      string
	Name    string
	Version string
}

// Install 从 zip 安装主题（D96 的九条校验，全部必做）。
//
// 校验顺序（先便宜后昂贵，且**任何一步失败都不落盘**）：
//  1. 压缩包大小 ≤ limits.MaxPackageBytes
//  2. 打开 zip（非法 → ErrArchiveInvalid）
//  3. 逐 entry：拒绝符号链接、拒绝越界路径（Zip Slip）、统计文件数与体积
//  4. 定位并解析 manifest.json（根或唯一顶层目录）
//  5. 运行清单七项校验（含 Pages），index.html 必须在包内
//  6. 目标目录存在且未 Overwrite → ErrThemeExists
//  7. 解压到 themesDir/.tmp-<随机>/（强制 0755/0644）
//  8. 原子性地 rename 到最终目录（失败回滚并清理临时目录）
//
// 审计（D96 第 9 条）在**本方法内**统一写入，成功与失败都写 ——
// 这样无论调用方是 HTTP handler 还是将来的 CLI，都不会漏记。
func (s *Service) Install(ctx context.Context, req InstallRequest) (*InstallResult, error) {
	res, err := s.install(req)
	if err != nil {
		s.auditor.Log(ctx, AuditEntry{
			Type:       model.LogTypeThemeInstall,
			Status:     model.LogStatusFailed,
			TargetType: "theme",
			Detail:     map[string]any{"PackageBytes": req.Size, "Overwrite": req.Overwrite},
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
			"Name": res.Name, "Version": res.Version,
			"PackageBytes": req.Size, "Overwrite": req.Overwrite,
		},
	})
	return res, nil
}

// install 是 Install 的实现体（不含审计，便于「先做事再统一记账」）。
func (s *Service) install(req InstallRequest) (*InstallResult, error) {
	limits := readLimits(s.settings)

	// ---- 1. 压缩包大小 ----
	if req.Size <= 0 {
		return nil, fmt.Errorf("%w: 空文件", ErrArchiveInvalid)
	}
	if req.Size > limits.MaxPackageBytes {
		return nil, fmt.Errorf("%w: %d > %d 字节", ErrPackageTooLarge, req.Size, limits.MaxPackageBytes)
	}

	// ---- 2. 打开 zip ----
	zr, err := zip.NewReader(req.File, req.Size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchiveInvalid, err)
	}
	if len(zr.File) > maxZipEntriesHardCap {
		return nil, fmt.Errorf("%w: entry 数 %d 超过硬上限 %d", ErrTooManyFiles, len(zr.File), maxZipEntriesHardCap)
	}

	// ---- 3. 逐 entry 校验 ----
	if err := validateZipEntries(zr.File, limits); err != nil {
		return nil, err
	}

	// ---- 4. 定位 manifest ----
	prefix, manifestEntry, err := locateManifest(zr.File)
	if err != nil {
		return nil, err
	}
	manifest, err := readZipManifest(manifestEntry, limits.MaxManifestBytes)
	if err != nil {
		return nil, err
	}

	// ---- 5. 清单七项校验 ----
	hasIndex := findZipEntry(zr.File, path.Join(prefix, IndexFile)) != nil
	if err := manifest.Validate(manifest.ID, hasIndex); err != nil {
		return nil, err
	}

	targetDir := s.store.DirOf(manifest.ID)

	// ---- 6. 冲突检查 ----
	// `default` 是兜底锚点：允许覆盖（用户可以用 make theme 的完整版替换内嵌副本），
	// 但必须显式勾选「覆盖」，避免误覆盖。
	if dirExists(targetDir) && !req.Overwrite {
		return nil, fmt.Errorf("%w: %s", ErrThemeExists, manifest.ID)
	}

	// ---- 7. 解压到临时目录 ----
	// 主题根目录可能还没建（例如首启 seed 被跳过、或数据卷被清空）——
	// 这里显式确保它存在，而不是让 MkdirTemp 报一个难懂的 stat 错误。
	if err := os.MkdirAll(s.store.ThemesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("创建主题目录失败: %w", err)
	}
	tmpDir, err := os.MkdirTemp(s.store.ThemesDir(), ".tmp-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	// 只要还没成功 rename，就确保临时目录被清掉（原子性 / 不留半个主题）
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err := extractZip(zr.File, prefix, tmpDir); err != nil {
		return nil, err
	}

	// ---- 8. 原子替换 ----
	if err := s.atomicReplace(tmpDir, targetDir); err != nil {
		return nil, err
	}
	keepTmp = true // tmpDir 已被 rename 走

	s.log.Info("主题安装成功",
		"id", manifest.ID, "version", manifest.Version,
		"package_bytes", req.Size, "overwrite", req.Overwrite,
	)
	return &InstallResult{ID: manifest.ID, Name: manifest.Name.Resolve("zh-CN"), Version: manifest.Version}, nil
}

// atomicReplace 把 src 目录原子性地放到 dst。
//
// 若 dst 已存在：先把 dst 改名为备份，再放 src，成功后才删备份；
// 放 src 失败则把备份改回来（D96 第 8 条：不支持「半个主题」这种状态）。
func (s *Service) atomicReplace(src, dst string) error {
	if !dirExists(dst) {
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("安装主题失败（rename）: %w", err)
		}
		return nil
	}

	backup := dst + ".bak-" + randomSuffix()
	if err := os.Rename(dst, backup); err != nil {
		return fmt.Errorf("替换主题失败（备份旧目录）: %w", err)
	}

	if err := os.Rename(src, dst); err != nil {
		// 回滚：把旧目录放回去
		if rbErr := os.Rename(backup, dst); rbErr != nil {
			s.log.Error("替换主题失败且回滚失败，请手工检查目录",
				"target", dst, "backup", backup, "err", err, "rollback_err", rbErr)
			return fmt.Errorf("替换主题失败且回滚失败（旧目录在 %s）: %w", backup, err)
		}
		return fmt.Errorf("替换主题失败（已回滚）: %w", err)
	}

	if err := os.RemoveAll(backup); err != nil {
		// 备份清理失败不影响安装结果，但要留痕
		s.log.Warn("清理主题备份目录失败", "backup", backup, "err", err)
	}
	return nil
}

// validateZipEntries 校验全部 entry：拒绝 symlink、拒绝越界路径、限制体积与数量。
func validateZipEntries(files []*zip.File, limits Limits) error {
	var total uint64
	var count int

	for _, f := range files {
		name := f.Name

		// 目录 entry 也参与 Zip Slip 检查（恶意包可能用目录名穿越）
		if _, err := safeJoin(".", name); err != nil {
			return fmt.Errorf("%w: %q", ErrZipSlip, name)
		}

		if f.FileInfo().IsDir() {
			continue
		}

		// 拒绝符号链接：防「先建软链再写到链外」
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q", ErrZipSymlink, name)
		}

		count++
		if count > limits.MaxFiles {
			return fmt.Errorf("%w: 超过 %d 个", ErrTooManyFiles, limits.MaxFiles)
		}

		size := int64(f.UncompressedSize64)
		if size > limits.MaxFileBytes {
			return fmt.Errorf("%w: %q 为 %d 字节（上限 %d）", ErrSingleFileTooLarge, name, size, limits.MaxFileBytes)
		}
		total += f.UncompressedSize64
		if int64(total) > limits.MaxExtractBytes {
			return fmt.Errorf("%w: 已超过 %d 字节", ErrExtractTooLarge, limits.MaxExtractBytes)
		}
	}
	return nil
}

// extractZip 把 zip 中的内容解压到 dstDir，并**去掉外层前缀目录**。
//
//   - 目录 `0755`、文件 `0644`：**忽略 zip 里声明的 mode**（防 setuid / 可执行位，D96 第 5 条）
//   - 每个 entry 再走一次越界检查（纵深防御）
func extractZip(files []*zip.File, prefix, dstDir string) error {
	for _, f := range files {
		rel, ok := stripZipPrefix(f.Name, prefix)
		if !ok || rel == "" {
			continue
		}

		target, err := safeJoin(dstDir, rel)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", target, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}

		if err := writeZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// writeZipFile 把单个 zip entry 写到磁盘（强制 0644）。
func writeZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开压缩项 %s 失败: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("创建文件 %s 失败: %w", target, err)
	}

	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return fmt.Errorf("写入文件 %s 失败: %w", target, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭文件 %s 失败: %w", target, err)
	}
	// 显式 chmod：即便 umask 或 zip 属性影响了创建模式，也强制到 0644
	if err := os.Chmod(target, 0o644); err != nil {
		return fmt.Errorf("设置文件权限 %s 失败: %w", target, err)
	}
	return nil
}

// locateManifest 找到 manifest.json 并返回它所在的外层前缀。
//
// 支持两种包结构（API.md §10 的安装校验第 4 条）：
//
//	manifest.json                    → prefix = ""
//	<唯一顶层目录>/manifest.json       → prefix = "<唯一顶层目录>"
func locateManifest(files []*zip.File) (string, *zip.File, error) {
	if f := findZipEntry(files, ManifestFile); f != nil {
		return "", f, nil
	}

	// 收集顶层目录名（忽略 macOS 打包常见的 __MACOSX 与点开头项）
	topDirs := map[string]struct{}{}
	for _, f := range files {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" {
			continue
		}
		first := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			first = name[:i]
		}
		if first == "" || strings.HasPrefix(first, ".") || first == "__MACOSX" {
			continue
		}
		topDirs[first] = struct{}{}
	}

	if len(topDirs) != 1 {
		return "", nil, fmt.Errorf("%w: %s 不在包根目录，且包内并非只有一个顶层目录",
			ErrManifestMissing, ManifestFile)
	}

	var top string
	for k := range topDirs {
		top = k
	}
	if f := findZipEntry(files, top+"/"+ManifestFile); f != nil {
		return top + "/", f, nil
	}
	return "", nil, fmt.Errorf("%w: %s", ErrManifestMissing, ManifestFile)
}

// findZipEntry 按规范化后的名字精确查找 entry（忽略目录 entry）。
func findZipEntry(files []*zip.File, name string) *zip.File {
	want := path.Clean(strings.TrimPrefix(name, "./"))
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		if path.Clean(f.Name) == want {
			return f
		}
	}
	return nil
}

// stripZipPrefix 去掉外层前缀目录。
func stripZipPrefix(name, prefix string) (string, bool) {
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if cleaned == "." {
		return "", false
	}
	if prefix == "" {
		return cleaned, true
	}
	trimmed := strings.TrimPrefix(cleaned, strings.TrimSuffix(prefix, "/")+"/")
	if trimmed == cleaned {
		// 不属于该前缀（例如被忽略的 __MACOSX）
		return "", false
	}
	return trimmed, true
}

// readZipManifest 读取并解析 manifest.json（带大小上限）。
func readZipManifest(f *zip.File, maxBytes int64) (*Manifest, error) {
	if maxBytes > 0 && int64(f.UncompressedSize64) > maxBytes {
		return nil, fmt.Errorf("%w: %d > %d 字节", ErrManifestTooLarge, f.UncompressedSize64, maxBytes)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", ManifestFile, err)
	}
	defer func() { _ = rc.Close() }()

	// 再读一层上限，防止 UncompressedSize64 与实际不符
	reader := io.Reader(rc)
	if maxBytes > 0 {
		reader = io.LimitReader(rc, maxBytes+1)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", ManifestFile, err)
	}
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("%w: %d > %d 字节", ErrManifestTooLarge, len(raw), maxBytes)
	}

	return ParseManifest(raw, maxBytes)
}
