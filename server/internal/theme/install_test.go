package theme

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// zipEntry 描述一个待写入 zip 的条目。
type zipEntry struct {
	name string
	body string
	// mode 为 0 时使用 0o644；显式设置可用于验证「忽略 zip 声明的 mode」。
	mode os.FileMode
	// symlinkTarget 非空时生成一个符号链接 entry（内容为链接目标）。
	symlinkTarget string
}

// buildZip 在内存里造一个 zip，并返回可随机读的句柄与精确长度。
func buildZip(t *testing.T, entries []zipEntry) (io.ReaderAt, int64) {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		if e.symlinkTarget != "" {
			mode = os.ModeSymlink | 0o777
		}

		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		hdr.SetMode(mode)

		fw, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("创建 zip 条目 %s 失败: %v", e.name, err)
		}
		body := e.body
		if e.symlinkTarget != "" {
			body = e.symlinkTarget
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatalf("写入 zip 条目 %s 失败: %v", e.name, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}

	data := buf.Bytes()
	// 复制一份，避免底层切片被复用
	cp := make([]byte, len(data))
	copy(cp, data)
	return bytes.NewReader(cp), int64(len(cp))
}

// validZipEntries 是一份能被正常安装的主题包（manifest 与 index.html 在包根）。
func validZipEntries(id string) []zipEntry {
	return []zipEntry{
		{
			name: ManifestFile,
			body: `{"ID":"` + id + `","Name":"` + id + `","Version":"1.2.3","Pages":["/"],
			  "Configuration":{"Type":"managed","Items":[{"Key":"K","Name":"k","Type":"string","Default":"v"}]}}`,
		},
		{name: IndexFile, body: "<html>" + themeIndexMarker + "</html>"},
		{name: "assets/app.js", body: "console.log(1)"},
	}
}

// installFromEntries 用给定条目执行一次安装。
func installFromEntries(t *testing.T, env *testEnv, entries []zipEntry, overwrite bool) (*InstallResult, error) {
	t.Helper()
	ra, size := buildZip(t, entries)
	return env.Svc.Install(t.Context(), InstallRequest{File: ra, Size: size, Overwrite: overwrite})
}

// ---- 正常路径 ----

func TestInstallValidZip(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env.Auditor.reset()

	res, err := installFromEntries(t, env, validZipEntries("mytheme"), false)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if res.ID != "mytheme" || res.Version != "1.2.3" {
		t.Errorf("安装结果不符: %#v", res)
	}

	// 目录已就位
	dir := filepath.Join(env.ThemesDir, "mytheme")
	for _, name := range []string{ManifestFile, IndexFile, filepath.Join("assets", "app.js")} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("安装后缺少 %s: %v", name, err)
		}
	}

	// 能被扫描到并激活
	if err := env.Svc.SetActive(t.Context(), "mytheme", "admin"); err != nil {
		t.Fatalf("安装后可启用: %v", err)
	}
	if got := env.Svc.Decide("/"); got.Target != TargetTheme || got.Theme.ID != "mytheme" {
		t.Errorf("安装的主题应接管首页，实际 %#v", got)
	}

	// 审计
	entries := env.Auditor.byType(model.LogTypeThemeInstall)
	if len(entries) != 1 || entries[0].Status != model.LogStatusSuccess {
		t.Fatalf("应写一条成功的 theme.install，实际 %#v", entries)
	}
}

func TestInstallAcceptsSingleTopLevelDir(t *testing.T) {
	env := newTestEnv(t)
	entries := make([]zipEntry, 0, 3)
	for _, e := range validZipEntries("wrapped") {
		entries = append(entries, zipEntry{name: "mytheme/" + e.name, body: e.body})
	}

	if _, err := installFromEntries(t, env, entries, false); err != nil {
		t.Fatalf("应支持「唯一顶层目录」结构: %v", err)
	}

	// 外层前缀必须被剥掉：manifest.json 与 index.html 应在主题根
	dir := filepath.Join(env.ThemesDir, "wrapped")
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err != nil {
		t.Errorf("外层目录未被剥掉: %v", err)
	}
}

func TestInstallOverwrite(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("mytheme", `"Pages":["/"]`, "OLD")

	// 不带 Overwrite → 冲突
	if _, err := installFromEntries(t, env, validZipEntries("mytheme"), false); !errors.Is(err, ErrThemeExists) {
		t.Fatalf("已存在且未勾选覆盖应返回 ErrThemeExists，实际 %v", err)
	}
	// 原内容不能被破坏
	if b, _ := os.ReadFile(filepath.Join(env.ThemesDir, "mytheme", IndexFile)); string(b) != "OLD" {
		t.Error("冲突时不应改动原目录")
	}

	// 带 Overwrite → 替换
	if _, err := installFromEntries(t, env, validZipEntries("mytheme"), true); err != nil {
		t.Fatalf("勾选覆盖后应安装成功: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(env.ThemesDir, "mytheme", IndexFile))
	if !strings.Contains(string(b), themeIndexMarker) {
		t.Errorf("覆盖后内容未更新: %q", string(b))
	}
}

// ---- 校验 1：包大小 ----

func TestInstallRejectsOversizedPackage(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyMaxPackageBytes, int64(64), "admin"); err != nil {
		t.Fatal(err)
	}

	_, err := installFromEntries(t, env, validZipEntries("big"), false)
	if !errors.Is(err, ErrPackageTooLarge) {
		t.Fatalf("应因包过大被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

// ---- 校验 2：Zip Slip ----

func TestInstallRejectsZipSlip(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slips := [][]string{
		{"../evil.txt"},
		{"../../evil.txt"},
		{"a/../../evil.txt"},
		{"/abs/evil.txt"},
		{"./../evil.txt"},
	}
	for _, names := range slips {
		for _, name := range names {
			entries := validZipEntries("slip")
			entries = append(entries, zipEntry{name: name, body: "PWNED"})

			if _, err := installFromEntries(t, env, entries, true); !errors.Is(err, ErrZipSlip) {
				t.Errorf("entry %q 应触发 ErrZipSlip，实际 %v", name, err)
			}
		}
	}

	// 关键：主题目录之外不能出现任何文件
	if _, err := os.Stat(filepath.Join(env.DataDir, "evil.txt")); err == nil {
		t.Error("安装包写出了主题目录之外的文件！")
	}
	assertNoResidue(t, env)
}

// ---- 校验 3：符号链接 ----

func TestInstallRejectsSymlinkEntry(t *testing.T) {
	env := newTestEnv(t)
	entries := validZipEntries("linked")
	entries = append(entries, zipEntry{
		name:          "assets/escape",
		symlinkTarget: "/etc/passwd",
	})

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrZipSymlink) {
		t.Fatalf("含 symlink 的包应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

// ---- 校验 4：体积与文件数 ----

func TestInstallRejectsOversizedSingleFile(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyMaxFileBytes, int64(100), "admin"); err != nil {
		t.Fatal(err)
	}

	entries := validZipEntries("fat")
	entries = append(entries, zipEntry{name: "assets/big.bin", body: strings.Repeat("X", 500)})

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrSingleFileTooLarge) {
		t.Fatalf("单文件超限应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

func TestInstallRejectsOversizedExtractTotal(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyMaxExtractBytes, int64(500), "admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.Settings.Set(KeyMaxFileBytes, int64(1000), "admin"); err != nil {
		t.Fatal(err)
	}

	entries := validZipEntries("bulk")
	for i := 0; i < 5; i++ {
		entries = append(entries, zipEntry{
			name: coreAssetName(i),
			body: strings.Repeat("Y", 300),
		})
	}

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrExtractTooLarge) {
		t.Fatalf("解压总体积超限应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

func coreAssetName(i int) string {
	return "assets/chunk" + string(rune('a'+i)) + ".bin"
}

func TestInstallRejectsTooManyFiles(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyMaxFiles, int64(3), "admin"); err != nil {
		t.Fatal(err)
	}

	entries := validZipEntries("many") // 已经 3 个文件
	entries = append(entries, zipEntry{name: "assets/extra.js", body: "x"})

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("文件数超限应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

// ---- 校验 6：manifest ----

func TestInstallRejectsMissingManifest(t *testing.T) {
	env := newTestEnv(t)
	entries := []zipEntry{{name: IndexFile, body: "<html/>"}}

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrManifestMissing) {
		t.Fatalf("缺 manifest 应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

func TestInstallRejectsMissingIndexHTML(t *testing.T) {
	env := newTestEnv(t)
	entries := []zipEntry{{
		name: ManifestFile,
		body: `{"ID":"noindex","Name":"n","Pages":["/"]}`,
	}}

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrIndexMissing) {
		t.Fatalf("缺 index.html 应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

func TestInstallRejectsOversizedManifest(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyMaxManifestBytes, int64(50), "admin"); err != nil {
		t.Fatal(err)
	}

	big := `{"ID":"big","Name":"` + strings.Repeat("n", 200) + `","Pages":["/"]}`
	entries := []zipEntry{
		{name: ManifestFile, body: big},
		{name: IndexFile, body: "<html/>"},
	}

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("manifest 超限应被拒，实际 %v", err)
	}
	assertNoResidue(t, env)
}

func TestInstallRejectsMultipleTopLevelDirs(t *testing.T) {
	env := newTestEnv(t)
	entries := []zipEntry{
		{name: "a/index.html", body: "<html/>"},
		{name: "b/whatever.txt", body: "x"},
	}

	if _, err := installFromEntries(t, env, entries, false); !errors.Is(err, ErrManifestMissing) {
		t.Fatalf("多顶层目录且无 manifest 应被拒，实际 %v", err)
	}
}

func TestInstallRejectsInvalidManifest(t *testing.T) {
	env := newTestEnv(t)

	cases := []struct {
		name string
		body string
	}{
		{"非法 JSON", "{"},
		{"ID 格式非法", `{"ID":"../x","Name":"n","Pages":["/"]}`},
		{"Name 为空", `{"ID":"x","Name":"","Pages":["/"]}`},
		{"Type 不支持", `{"ID":"x","Name":"n","Configuration":{"Type":"raw"}}`},
		{"Pages 命中认证页", `{"ID":"x","Name":"n","Pages":["/login"]}`},
		{"Pages 命中后台", `{"ID":"x","Name":"n","Pages":["/admin"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []zipEntry{
				{name: ManifestFile, body: tc.body},
				{name: IndexFile, body: "<html/>"},
			}
			_, err := installFromEntries(t, env, entries, true)
			if err == nil {
				t.Fatal("应当被拒绝")
			}
			if !errors.Is(err, ErrThemeInvalid) && !errors.Is(err, ErrManifestInvalid) {
				t.Errorf("错误类型不符: %v", err)
			}
			assertNoResidue(t, env)
		})
	}
}

// ---- 校验 5：强制权限位 ----

func TestInstallForcesPermissionBits(t *testing.T) {
	env := newTestEnv(t)

	// zip 里声明 0777（含可执行位）；安装后必须是 0644
	entries := []zipEntry{
		{name: ManifestFile, body: `{"ID":"perm","Name":"n","Pages":["/"]}`, mode: 0o777},
		{name: IndexFile, body: "<html/>", mode: 0o777},
		{name: "assets/x.sh", body: "#!/bin/sh", mode: 0o777},
	}
	if _, err := installFromEntries(t, env, entries, false); err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	for _, rel := range []string{ManifestFile, IndexFile, filepath.Join("assets", "x.sh")} {
		info, err := os.Stat(filepath.Join(env.ThemesDir, "perm", rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("%s 权限应为 0644，实际 %o（zip 声明的 mode 必须被忽略）", rel, perm)
		}
		if info.Mode()&os.ModeSetuid != 0 || info.Mode()&os.ModeSetgid != 0 {
			t.Errorf("%s 不应带 setuid/setgid 位", rel)
		}
	}
}

// ---- 校验 8：原子性 ----

func TestInstallFailureLeavesNoResidue(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	// 让安装中途失败：包里有 symlink（在解压前的校验阶段就会失败）
	entries := validZipEntries("atomic")
	entries = append(entries, zipEntry{name: "assets/link", symlinkTarget: "/etc/hosts"})

	if _, err := installFromEntries(t, env, entries, false); err == nil {
		t.Fatal("应当失败")
	}

	// 目标目录不应存在
	if dirExists(filepath.Join(env.ThemesDir, "atomic")) {
		t.Error("失败后不应留下半个主题")
	}
	// 不应留下临时目录
	assertNoResidue(t, env)
}

func assertNoResidue(t *testing.T, env *testEnv) {
	t.Helper()

	entries, err := os.ReadDir(env.ThemesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatalf("读取主题目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("残留了临时目录: %s", e.Name())
		}
		if strings.HasSuffix(e.Name(), ".bak-") || strings.Contains(e.Name(), ".bak-") {
			t.Errorf("残留了备份目录: %s", e.Name())
		}
	}
}

// ---- 校验 9：审计 ----

func TestInstallWritesAuditOnFailure(t *testing.T) {
	env := newTestEnv(t)
	env.Auditor.reset()

	entries := []zipEntry{{name: IndexFile, body: "<html/>"}} // 缺 manifest
	ra, size := buildZip(t, entries)
	// 直接调 Service.Install（handler 才写失败审计），这里只确认错误可判定
	if _, err := env.Svc.Install(t.Context(), InstallRequest{File: ra, Size: size}); err == nil {
		t.Fatal("应当失败")
	}
	// 失败也必须记账（D96 第 9 条），且带失败原因
	entries2 := env.Auditor.byType(model.LogTypeThemeInstall)
	if len(entries2) != 1 {
		t.Fatalf("失败应写一条 theme.install，实际 %d", len(entries2))
	}
	if entries2[0].Status != model.LogStatusFailed || entries2[0].Cause == nil {
		t.Errorf("失败条目应带 Status=failed 与 Cause，实际 %#v", entries2[0])
	}
}

// ---- 其它 ----

func TestInstallRejectsEmptyAndGarbagePackage(t *testing.T) {
	env := newTestEnv(t)

	if _, err := env.Svc.Install(t.Context(), InstallRequest{File: bytes.NewReader(nil), Size: 0}); !errors.Is(err, ErrArchiveInvalid) {
		t.Errorf("空包应被拒，实际 %v", err)
	}

	garbage := []byte("this is not a zip file at all")
	if _, err := env.Svc.Install(t.Context(), InstallRequest{
		File: bytes.NewReader(garbage), Size: int64(len(garbage)),
	}); !errors.Is(err, ErrArchiveInvalid) {
		t.Errorf("非法 zip 应被拒，实际 %v", err)
	}
}

func TestInstallIgnoresMacOSXMetadata(t *testing.T) {
	env := newTestEnv(t)
	entries := validZipEntries("macos")
	// 只多一个 __MACOSX 顶层条目：不应影响「唯一顶层目录」的判断
	entries = append(entries, zipEntry{name: "__MACOSX/._manifest.json", body: "junk"})

	if _, err := installFromEntries(t, env, entries, false); err != nil {
		t.Fatalf("__MACOSX 元数据不应导致失败: %v", err)
	}
}
