package theme

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// embeddedFS 是**随二进制发布**的默认主题。
//
// 它承担两个职责（OPERATIONS §8.3 / §8.7）：
//
//  1. **seed 源**：首启时若 <dataDir>/themes/ 为空，把它解压一份到
//     <dataDir>/themes/default/，让用户有一个可编辑的起点；
//  2. **兜底锚点**：磁盘上的主题缺失 / 损坏 / Pages 非法时，直接用这份渲染，
//     保证首页**永不白屏**（即使整个 data/themes/ 被删掉）。
//
// 与 `make theme` 的关系：`make theme` 会用 web/theme-default/ 构建出「完整版」
// 默认主题覆盖到 <dataDir>/themes/default/；本文件是**兜底副本**，不参与构建。
//
//go:embed all:embedded
var embeddedFS embed.FS

// embeddedDefaultID 是内嵌默认主题的 ID（与目录名一致，满足 D98 校验第 1 条）。
const embeddedDefaultID = "default"

// EmbeddedDefaultID 返回内嵌默认主题的 ID。
func EmbeddedDefaultID() string { return embeddedDefaultID }

// embeddedRoot 返回内嵌默认主题的根路径。
func embeddedRoot() string { return "embedded" }

// EmbeddedFS 返回内嵌默认主题的文件系统（根即主题目录，含 manifest.json 与 index.html）。
//
// 供 Store 解析清单、读取 index.html/资源与 seed 使用。
func EmbeddedFS() (fs.FS, error) {
	sub, err := fs.Sub(embeddedFS, embeddedRoot())
	if err != nil {
		return nil, fmt.Errorf("读取内嵌默认主题失败: %w", err)
	}
	return sub, nil
}

// embeddedFile 读取内嵌默认主题中的一个文件。
func embeddedFile(name string) ([]byte, error) {
	sub, err := EmbeddedFS()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(sub, name)
}

// seedIfEmpty 在 <themesDir> 为空时，写出默认主题到 <themesDir>/default/。
//
// 「为空」= 目录不存在，或没有任何子目录（忽略点开头的临时目录）。
// 非空时**什么都不做** —— 升级只重跑容器，绝不覆盖用户放进去的主题（D94.4）。
//
// seedFrom 非空且存在时，从该目录复制（用于 `PICGO_WEB_THEME_SEED`：
// 部署时把 `make theme` 产出的「完整版」默认主题种进去）；
// 否则回退到**随二进制内嵌**的默认主题。
//
// 返回 (是否执行了 seed, 错误)。
func seedIfEmpty(themesDir, seedFrom string) (bool, error) {
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		return false, fmt.Errorf("创建主题目录 %s 失败: %w", themesDir, err)
	}

	empty, err := dirHasNoSubdir(themesDir)
	if err != nil {
		return false, err
	}
	if !empty {
		return false, nil
	}

	target := filepath.Join(themesDir, embeddedDefaultID)

	// 优先使用外部 seed 源（若配置了且确实存在）
	if src := strings.TrimSpace(seedFrom); src != "" {
		if dirExists(src) {
			if err := copyThemeDir(src, target); err != nil {
				return false, fmt.Errorf("从 %s 复制默认主题失败: %w", src, err)
			}
			return true, nil
		}
		// 配置了但不存在：不静默忽略，明确告知（否则运维会以为生效了）
		return false, fmt.Errorf("seed 源目录不存在: %s", src)
	}

	if err := writeEmbeddedTheme(target); err != nil {
		return false, err
	}
	return true, nil
}

// copyThemeDir 把 src 目录的内容复制到 dst（权限位固定 0755 / 0644）。
//
// 每个条目都走 safeJoin 检查，因此即使 src 里被塞了符号链接也不会把内容
// 写到目标之外（符号链接本身按普通文件复制其链接目标文本——不会跟随）。
func copyThemeDir(src, dst string) error {
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

		target, joinErr := safeJoin(dst, rel)
		if joinErr != nil {
			return joinErr
		}

		// 符号链接按普通文件处理（读链接目标文本），不跟随
		if d.Type()&os.ModeSymlink != 0 {
			link, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			return os.WriteFile(target, []byte(link), 0o644)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// dirHasNoSubdir 判断目录下是否没有任何子目录（点开头的临时项不算）。
func dirHasNoSubdir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("读取主题目录 %s 失败: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if isTempName(e.Name()) {
			continue
		}
		return false, nil
	}
	return true, nil
}

// isTempName 判断是否是安装过程中的临时目录（`.tmp-xxx`）或点开头的隐藏项。
func isTempName(name string) bool {
	if name == "" {
		return false
	}
	return name[0] == '.'
}

// writeEmbeddedTheme 把内嵌默认主题的内容写出到 dstDir。
//
// 权限位固定为 0755 / 0644（与 zip 安装一致，避免权限来源不一致）。
func writeEmbeddedTheme(dstDir string) error {
	sub, err := EmbeddedFS()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dstDir, AssetsDir), 0o755); err != nil {
		return fmt.Errorf("创建主题目录失败: %w", err)
	}

	walkErr := fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		target, err := safeJoin(dstDir, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		return fmt.Errorf("写出内嵌默认主题失败: %w", walkErr)
	}
	return nil
}

// safeJoin 把相对路径安全地拼到 base 下，拒绝越界（与 zip 安装同一套判断）。
//
// 用 filepath.Rel 而不是 strings.HasPrefix：后者在 base 本身、盘符、UNC、
// Windows 大小写等边界会误判（这是相对 Komari 的一处硬化，见 D96）。
func safeJoin(base, rel string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: %q 是绝对路径", ErrZipSlip, rel)
	}

	target := filepath.Join(base, cleaned)
	if !isInside(base, target) {
		return "", fmt.Errorf("%w: %q 越出目标目录", ErrZipSlip, rel)
	}
	return target, nil
}

// isInside 判断 target 是否落在 base 之内（含 base 自身）。
//
// 用 filepath.Rel 而不是 strings.HasPrefix：后者在 base 本身、盘符、UNC、
// Windows 大小写等边界会误判（D96「比 Komari 更严的两处」之一）。
func isInside(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}

	// base 自身
	if rel == "." {
		return true
	}
	// 越界：`..` 或 `../xxx`
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
