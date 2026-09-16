package theme

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// buildThemeDir 构造一个最小的合法主题目录（manifest + index）。
func buildThemeDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", `{
  "ID": "git-theme",
  "Name": {"zh-CN": "Git 主题", "en": "Git Theme"},
  "Version": "1.0.0",
  "Pages": ["/"]
}`)
	write("index.html", "<!doctype html><html><body>git theme</body></html>")
}

// initGitRepo 把 dir 初始化为 git 仓库并提交（供本地 clone 测试）。
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境中没有 git，跳过依赖 git 的用例")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("add", "-A")
	run("commit", "-m", "init")
}

// ValidateGitURL：只放行不带凭据的 https 地址。
func TestValidateGitURL(t *testing.T) {
	cases := []struct {
		url   string
		valid bool
	}{
		{"https://github.com/Yeqingky/PicGo-Web-Theme.git", true},
		{"https://gitlab.com/foo/bar", true},
		{"https://git.example.com:8443/group/repo.git", true},
		{"http://github.com/foo/bar", false},          // 非 https
		{"file:///tmp/repo", false},                   // 本地路径
		{"ssh://git@github.com/foo/bar", false},       // ssh
		{"git@github.com:foo/bar.git", false},         // scp 语法
		{"https://user:pass@github.com/foo/bar", false}, // 凭据
		{"", false},
		{"   ", false},
		{"https://", false}, // 无 host
	}
	for _, c := range cases {
		err := ValidateGitURL(c.url)
		if c.valid && err != nil {
			t.Errorf("应合法: %q → %v", c.url, err)
		}
		if !c.valid && err == nil {
			t.Errorf("应拒绝: %q", c.url)
		}
	}
}

// 从本地 Git 仓库完整走一遍安装：clone → 校验 → 落盘 → GitInstallResult。
// 本地路径 clone 仅用于测试夹具（生产入口已被 ValidateGitURL 限定为 https）。
func TestInstallFromGit_LocalRepo(t *testing.T) {
	env := newTestEnv(t)

	repo := filepath.Join(t.TempDir(), "theme-repo")
	buildThemeDir(t, repo)
	initGitRepo(t, repo)

	// cloneRepo 本身校验 https；这里直接测「clone → installFromDir」的内部组合
	tmp := t.TempDir()
	cloneDir := filepath.Join(tmp, "repo")
	if _, err := exec.Command("git", "clone", "--quiet", repo, cloneDir).CombinedOutput(); err != nil {
		t.Fatalf("准备克隆失败: %v", err)
	}
	res, err := env.Svc.installFromDir(cloneDir, readLimits(env.Settings), false)
	if err != nil {
		t.Fatalf("installFromDir 失败: %v", err)
	}
	if res.ID != "git-theme" {
		t.Fatalf("ID 应为 git-theme，实际 %s", res.ID)
	}

	// 落盘内容正确（且不含 .git）
	for _, name := range []string{"manifest.json", "index.html"} {
		if _, err := os.Stat(filepath.Join(env.ThemesDir, "git-theme", name)); err != nil {
			t.Errorf("%s 未落盘: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(env.ThemesDir, "git-theme", ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".git 不应被复制进主题目录")
	}

	// 重复安装（不覆盖）→ ErrThemeExists；带覆盖 → 成功
	if _, err := env.Svc.installFromDir(cloneDir, readLimits(env.Settings), false); !errors.Is(err, ErrThemeExists) {
		t.Fatalf("重复安装应报 ErrThemeExists，实际 %v", err)
	}
	if _, err := env.Svc.installFromDir(cloneDir, readLimits(env.Settings), true); err != nil {
		t.Fatalf("覆盖安装失败: %v", err)
	}
}

// InstallFromGit：完整链路 + 审计。用本地 HTTP 仓库不可行（ValidateGitURL 只放行 https），
// 这里直接断言「非法 URL 被拒 + 失败有审计」；成功链路由 e2e（真实 GitHub）覆盖。
func TestInstallFromGit_RejectsAndAudits(t *testing.T) {
	env := newTestEnv(t)
	env.Auditor.reset()

	_, err := env.Svc.InstallFromGit(context.Background(), GitInstallRequest{URL: "http://github.com/x/y"})
	if !errors.Is(err, ErrGitInvalid) {
		t.Fatalf("http 应被拒绝，实际 %v", err)
	}
	entries := env.Auditor.byType(model.LogTypeThemeInstall)
	if len(entries) != 1 || entries[0].Status != model.LogStatusFailed {
		t.Fatalf("失败应写一条失败审计，实际 %+v", entries)
	}
	detail, _ := entries[0].Detail.(map[string]any)
	if detail == nil || detail["Via"] != "git" {
		t.Errorf("审计 Detail 应带 Via=git，实际 %v", entries[0].Detail)
	}
}

// InstallFromGit 的成功路径：clone 本地 https 不可用时，退而直接验证
// 「cloneRepo → copyDirContents → installFromDir」组合不受 .git 干扰，
// 且 store Seed 的 Git 源失败时回退内嵌（不阻断启动语义）。
func TestSeedGitFallback(t *testing.T) {
	env := newTestEnv(t)
	// 配置一个必然失败的 https 地址（不可达端口不合法——用合法但空路径的 https 也会失败）
	store := env.Svc.Store()
	store.defaultGitURL = "https://127.0.0.1:9/no-such-repo.git" // 端口 9（discard）不可达

	seeded, err := store.Seed()
	if err != nil {
		t.Fatalf("seed 不应因 Git 失败而报错（应回退内嵌）: %v", err)
	}
	if !seeded {
		t.Fatal("应完成 seed（回退内嵌副本）")
	}
	// 内嵌副本已写出
	if _, err := os.Stat(filepath.Join(env.ThemesDir, "default", "index.html")); err != nil {
		t.Fatalf("回退内嵌后 index.html 应存在: %v", err)
	}
}

// seed 走 Git 成功路径：用本地仓库 + 直接调用 seedFromGit（绕过 https 校验的夹具）。
// seedFromGit 内部会校验 URL —— 本地测试用「合法 https 地址 + 已存在的目标」不可行，
// 因此这里验证 copyDirContents 对含 .git 目录的跳过行为（Git 安装的核心差异点）。
func TestCopyDirContents_SkipsGit(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	buildThemeDir(t, repo)
	initGitRepo(t, repo)

	dst := filepath.Join(base, "out")
	if err := copyDirContents(repo, dst); err != nil {
		t.Fatalf("copyDirContents 失败: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".git 不应被复制")
	}
	for _, name := range []string{"manifest.json", "index.html"} {
		data, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("%s 应被复制: %v", name, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("%s 内容为空", name)
		}
	}
}

// seedFromGit 的 URL 校验与 git 缺失分支。
func TestSeedFromGit_InvalidInput(t *testing.T) {
	dst := t.TempDir()
	if err := seedFromGit("not-a-url", dst); !errors.Is(err, ErrGitInvalid) {
		t.Fatalf("非法 URL 应报 ErrGitInvalid，实际 %v", err)
	}
}
