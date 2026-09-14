package crypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := New(key)
	if err != nil {
		t.Fatalf("构造 Cipher 失败: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newTestCipher(t)

	cases := []string{
		"",
		"a",
		"hello world",
		"中文与 emoji 🐱 混排",
		strings.Repeat("x", 10000),
		`{"token":"ghp_xxx","secret":"s3cr3t"}`,
	}

	for _, plain := range cases {
		enc, err := c.EncryptString(plain)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plain[:min(len(plain), 20)], err)
		}
		if plain != "" && enc == plain {
			t.Errorf("密文与明文相同: %q", plain)
		}

		got, err := c.DecryptString(enc)
		if err != nil {
			t.Fatalf("解密失败: %v", err)
		}
		if got != plain {
			t.Errorf("往返不一致\n got = %q\nwant = %q", got, plain)
		}
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	// 每次加密必须使用随机 nonce，否则相同明文会产生相同密文（泄露相等性）
	c := newTestCipher(t)
	plain := "same plaintext"

	a, err := c.EncryptString(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.EncryptString(plain)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("两次加密结果相同：nonce 未随机化")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c1 := newTestCipher(t)

	key2 := make([]byte, KeySize)
	for i := range key2 {
		key2[i] = byte(i + 100)
	}
	c2, err := New(key2)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c1.EncryptString("secret data")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c2.DecryptString(enc); err == nil {
		t.Fatal("用错误密钥解密应当失败")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	c := newTestCipher(t)
	enc, err := c.EncryptString("secret data")
	if err != nil {
		t.Fatal(err)
	}

	// 翻转最后一个字符（改动密文）
	b := []byte(enc)
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}

	if _, err := c.DecryptString(string(b)); err == nil {
		t.Fatal("被篡改的密文解密应当失败（GCM 认证）")
	}
}

func TestDecryptBadInput(t *testing.T) {
	c := newTestCipher(t)
	for _, bad := range []string{"", "not-base64!!!", "YWJj"} { // 最后一个是合法 base64 但太短
		if _, err := c.DecryptString(bad); err == nil {
			t.Errorf("非法输入 %q 应当报错", bad)
		}
	}
}

func TestNewKeyValidation(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("空密钥应当报错")
	}
	if _, err := New(make([]byte, 16)); err == nil {
		t.Error("16 字节密钥应当报错（需要 32）")
	}
	if _, err := New(make([]byte, KeySize)); err != nil {
		t.Errorf("32 字节密钥应当成功: %v", err)
	}
}

func TestNewFromString(t *testing.T) {
	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i)
	}

	// 三种输入形式都应被接受
	forms := []string{
		string(raw), // 原样 32 字节
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", // hex
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",                     // base64
	}
	for i, f := range forms {
		c, err := NewFromString(f)
		if err != nil {
			t.Errorf("形式 %d 解析失败: %v", i, err)
			continue
		}
		if len(c.key) != KeySize {
			t.Errorf("形式 %d 密钥长度不是 %d", i, KeySize)
		}
	}

	if _, err := NewFromString(""); err == nil {
		t.Error("空字符串应当报错")
	}
	if _, err := NewFromString("short"); err == nil {
		t.Error("长度不对的字符串应当报错")
	}
}

func TestLoadOrCreateKeyGeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "secret.key")

	// 第一次：生成
	k1, generated, err := LoadOrCreateKey("", keyFile)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if !generated {
		t.Error("首次调用应当标记为已生成")
	}
	if len(k1) != KeySize {
		t.Errorf("密钥长度应为 %d，实际 %d", KeySize, len(k1))
	}

	// 文件权限必须是 0600（主密钥不能给他人读）
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("密钥文件未创建: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("密钥文件权限应为 0600，实际 %o", perm)
	}

	// 第二次：复用
	k2, generated2, err := LoadOrCreateKey("", keyFile)
	if err != nil {
		t.Fatalf("读取密钥失败: %v", err)
	}
	if generated2 {
		t.Error("第二次调用不应再生成")
	}
	if string(k1) != string(k2) {
		t.Error("两次读取的密钥不一致")
	}

	// 环境变量优先
	envKey := strings.Repeat("A", KeySize)
	k3, generated3, err := LoadOrCreateKey(envKey, keyFile)
	if err != nil {
		t.Fatalf("环境变量密钥读取失败: %v", err)
	}
	if generated3 {
		t.Error("使用环境变量时不应标记为已生成")
	}
	if string(k3) != envKey {
		t.Error("环境变量密钥未被优先使用")
	}
}

func TestMaskHelpers(t *testing.T) {
	if !IsMasked(Mask) {
		t.Error("Mask 应被 IsMasked 识别")
	}
	if IsMasked("real-secret") {
		t.Error("真实值不应被识别为掩码")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
