package auth

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
)

// ---- 密码（bcrypt）----

func TestPasswordHashRoundTrip(t *testing.T) {
	const pw = "S3cret-Passw0rd!"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword 失败: %v", err)
	}
	if hash == pw {
		t.Fatal("哈希与明文相同")
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("不是 bcrypt 格式: %s", hash)
	}
	if !VerifyPassword(hash, pw) {
		t.Error("正确密码校验失败")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Error("错误密码竟然通过校验")
	}
	// 空哈希（纯 OAuth 用户）必须一律失败
	if VerifyPassword("", pw) {
		t.Error("空哈希不应通过校验")
	}
}

func TestPasswordHashIsSalted(t *testing.T) {
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("两次哈希结果相同：bcrypt 未加盐")
	}
	if !VerifyPassword(h1, "same-password") || !VerifyPassword(h2, "same-password") {
		t.Error("加盐后仍应能校验通过")
	}
}

func TestBcryptCostIsTwelve(t *testing.T) {
	hash, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	// 形如 $2a$12$...
	parts := strings.Split(hash, "$")
	if len(parts) < 3 {
		t.Fatalf("哈希格式异常: %s", hash)
	}
	if parts[2] != "12" {
		t.Errorf("bcrypt cost 应为 12（D24），实际 %s", parts[2])
	}
}

func TestSpendDummyCompareDoesNotPanic(t *testing.T) {
	// 防枚举路径：不存在邮箱 / 无密码账号都会走这里
	SpendDummyCompare("any-password")
	SpendDummyCompare("")
}

func TestGenerateRandomPassword(t *testing.T) {
	const n = 16
	pw, err := GenerateRandomPassword(n)
	if err != nil {
		t.Fatalf("生成随机密码失败: %v", err)
	}
	if len(pw) != n {
		t.Errorf("长度应为 %d，实际 %d（%s）", n, len(pw), pw)
	}
	// 不应含易混淆字符
	if strings.ContainsAny(pw, "0O1lI") {
		t.Errorf("随机密码含易混淆字符: %s", pw)
	}

	// 不应重复
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := GenerateRandomPassword(12)
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatalf("生成的密码重复: %s", p)
		}
		seen[p] = true
	}

	if _, err := GenerateRandomPassword(0); err == nil {
		t.Error("长度为 0 应报错")
	}
}

// ---- JWT ----

func TestJWTSignAndVerify(t *testing.T) {
	m := NewJWTManager([]byte("0123456789abcdef0123456789abcdef"))
	const uid = "usr_01ABCDEF"

	token, expiresIn, err := m.Sign(uid, "admin", 15*time.Minute)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if expiresIn != int64((15 * time.Minute).Seconds()) {
		t.Errorf("ExpiresIn 应为 900，实际 %d", expiresIn)
	}

	claims, err := m.Verify(token)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if claims.Subject != uid {
		t.Errorf("sub 应为 %s，实际 %s", uid, claims.Subject)
	}
	if claims.Role != "admin" {
		t.Errorf("role 应为 admin，实际 %s", claims.Role)
	}
	if claims.Typ != TokenTypeAccess {
		t.Errorf("typ 应为 %s，实际 %s", TokenTypeAccess, claims.Typ)
	}
}

func TestJWTRejectsForeignKey(t *testing.T) {
	m1 := NewJWTManager([]byte("key-number-one-0123456789abcdef"))
	m2 := NewJWTManager([]byte("key-number-two-0123456789abcdef"))

	token, _, err := m1.Sign("usr_x", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Verify(token); err == nil {
		t.Error("用另一把主密钥派生出的密钥不应能校验通过")
	}
}

func TestJWTRejectsTamperedToken(t *testing.T) {
	m := NewJWTManager([]byte("0123456789abcdef0123456789abcdef"))
	token, _, err := m.Sign("usr_x", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 改动 payload 部分
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 结构异常: %s", token)
	}
	tampered := parts[0] + "." + parts[1][:len(parts[1])-2] + "AA." + parts[2]
	if _, err := m.Verify(tampered); err == nil {
		t.Error("被篡改的令牌不应通过校验")
	}
	if _, err := m.Verify("not-a-jwt"); err == nil {
		t.Error("非法格式不应通过校验")
	}
	if _, err := m.Verify(""); err == nil {
		t.Error("空令牌不应通过校验")
	}
}

func TestJWTExpiry(t *testing.T) {
	m := NewJWTManager([]byte("0123456789abcdef0123456789abcdef"))

	// 直接构造一个已过期的令牌（同一包内可访问 m.key），避免测试里 sleep
	claims := Claims{
		Role: "user",
		Typ:  TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "usr_expired",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.key)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Verify(expired); err != ErrTokenExpired {
		t.Errorf("过期令牌应返回 ErrTokenExpired，实际 %v", err)
	}
}

func TestJWTRejectsWrongTyp(t *testing.T) {
	m := NewJWTManager([]byte("0123456789abcdef0123456789abcdef"))

	// typ 不是 access 的 JWT（例如将来可能的其它用途令牌）不能当 access token 用
	claims := Claims{
		Role: "user",
		Typ:  "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "usr_x",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(token); err != ErrTokenInvalid {
		t.Errorf("typ 不符应返回 ErrTokenInvalid，实际 %v", err)
	}
}

func TestJWTRejectsEmptySubject(t *testing.T) {
	m := NewJWTManager([]byte("0123456789abcdef0123456789abcdef"))
	if _, _, err := m.Sign("", "user", time.Minute); err == nil {
		t.Error("空 UID 签发应报错")
	}
}

// ---- 令牌 ----

func TestHashTokenIsDeterministic(t *testing.T) {
	h1 := HashToken("pcw_abcdef")
	h2 := HashToken("pcw_abcdef")
	if h1 != h2 {
		t.Error("同一输入应得到相同摘要")
	}
	if len(h1) != 64 {
		t.Errorf("SHA-256 十六进制应为 64 字符，实际 %d", len(h1))
	}
	if HashToken("pcw_abcdef") == HashToken("pcw_abcdeg") {
		t.Error("不同输入不应得到相同摘要")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	plain, hash, err := GenerateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if plain == "" || hash == "" {
		t.Fatal("令牌与摘要不应为空")
	}
	if hash != HashToken(plain) {
		t.Error("返回的摘要与明文不匹配")
	}
	if len(plain) < 40 {
		t.Errorf("refresh token 过短（%d 字符），随机性不足", len(plain))
	}

	p2, _, err := GenerateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if p2 == plain {
		t.Error("两次生成的 refresh token 相同")
	}
}

func TestGenerateAPIToken(t *testing.T) {
	plain, hash, prefix, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, APITokenPrefix) {
		t.Errorf("缺少 %s 前缀: %s", APITokenPrefix, plain)
	}
	if hash != HashToken(plain) {
		t.Error("摘要与明文不匹配")
	}
	if !strings.HasPrefix(plain, prefix) {
		t.Errorf("prefix(%s) 应为明文的前缀", prefix)
	}
	if len(prefix) != 12 { // "pcw_" + 8
		t.Errorf("prefix 长度应为 12，实际 %d（%s）", len(prefix), prefix)
	}
	if !IsAPIToken(plain) {
		t.Error("IsAPIToken 应识别生成的令牌")
	}
	if IsAPIToken("eyJhbGciOiJIUzI1NiJ9.x.y") {
		t.Error("JWT 不应被识别为 API token")
	}
}

// ---- OAuth state ----

func TestStateSignAndVerify(t *testing.T) {
	s := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))

	state, err := s.Sign(StatePayload{
		Redirect: "/gallery",
		Intent:   StateIntentLogin,
	})
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}

	payload, err := s.Verify(state, StateTTL)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if payload.Redirect != "/gallery" || payload.Intent != StateIntentLogin {
		t.Errorf("载荷不一致: %+v", payload)
	}
	if payload.Nonce == "" {
		t.Error("应自动生成 nonce")
	}
}

func TestStateRejectsTampered(t *testing.T) {
	s := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))

	state, err := s.Sign(StatePayload{Redirect: "/a", Intent: StateIntentLogin})
	if err != nil {
		t.Fatal(err)
	}

	// 改动签名
	if _, err := s.Verify(strings.TrimSuffix(state, state[len(state)-1:])+"X", StateTTL); err == nil {
		t.Error("被篡改的签名不应通过")
	}
	// 换一把密钥
	other := NewStateSigner([]byte("another-key-0123456789abcdefghij"))
	if _, err := other.Verify(state, StateTTL); err == nil {
		t.Error("另一把密钥签的 state 不应通过")
	}
	// 非法格式
	for _, bad := range []string{"", ".", "abc", "a.b"} {
		if _, err := s.Verify(bad, StateTTL); err == nil {
			t.Errorf("非法 state %q 不应通过", bad)
		}
	}
}

func TestStateExpiry(t *testing.T) {
	s := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))

	// 手工构造一个 10 分钟前签发的 state
	state, err := s.Sign(StatePayload{
		Redirect: "/",
		Intent:   StateIntentLogin,
		IssuedAt: time.Now().Add(-10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(state, StateTTL); err != ErrStateExpired {
		t.Errorf("超期 state 应返回 ErrStateExpired，实际 %v", err)
	}
}

func TestStateRejectsUnknownIntent(t *testing.T) {
	s := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))
	state, err := s.Sign(StatePayload{Redirect: "/", Intent: StateIntent("evil")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(state, StateTTL); err == nil {
		t.Error("未知 intent 不应通过")
	}
}

func TestSanitizeRedirect(t *testing.T) {
	cases := []struct {
		in   string
		want string
		note string
	}{
		{"", "/", "空值回退"},
		{"/gallery", "/gallery", "站内路径"},
		{"/gallery?Page=2", "/gallery", "丢掉 query"},
		{"/gallery#frag", "/gallery", "丢掉 fragment"},
		{"//evil.com", "/", "协议相对 URL 必须拒绝"},
		{"https://evil.com", "/", "绝对 URL 必须拒绝"},
		{"http://evil.com/path", "/", "绝对 URL 必须拒绝"},
		{"/a/../b", "/", "路径穿越必须拒绝"},
		{"/a/..", "/", "路径穿越必须拒绝"},
		{"/\\evil", "/", "反斜杠必须拒绝"},
		{"/ok/../ok2", "/", "含 .. 即拒绝（保守策略）"},
		{"/upload", "/upload", "普通路径"},
	}

	for _, tc := range cases {
		got := SanitizeRedirect(tc.in, "/")
		if got != tc.want {
			t.Errorf("SanitizeRedirect(%q) = %q，期望 %q（%s）", tc.in, got, tc.want, tc.note)
		}
	}
}

// ---- 首启引导 ----

func newTestUserRepo(t *testing.T) *repository.UserRepo {
	t.Helper()

	dir := t.TempDir()
	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "auth.db"),
		DataDir:        dir,
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := database.Open(cfg, log)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return repository.NewUserRepo(db.DB)
}

func TestBootstrapCreatesAdminOnce(t *testing.T) {
	repo := newTestUserRepo(t)
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 第一次：应创建
	if err := Bootstrap(repo, cfg, log); err != nil {
		t.Fatalf("首次引导失败: %v", err)
	}

	admin, err := repo.FindByEmail(BootstrapAdminEmail)
	if err != nil {
		t.Fatalf("未创建管理员: %v", err)
	}
	if admin.Role != "admin" {
		t.Errorf("角色应为 admin，实际 %s", admin.Role)
	}
	if admin.Status != "active" {
		t.Errorf("状态应为 active，实际 %s", admin.Status)
	}
	if !admin.MustChangePassword {
		t.Error("初始管理员必须强制首次改密（D32）")
	}
	if admin.PasswordHash == "" {
		t.Error("必须设置密码哈希")
	}
	if admin.CapacityBytes != 0 {
		t.Errorf("管理员应为不限额（0），实际 %d", admin.CapacityBytes)
	}

	// Profile 也要建
	if _, err := repo.FindProfile(admin.UID); err != nil {
		t.Errorf("未创建 UserProfile: %v", err)
	}

	// 密码文件：存在且 0600
	pwFile := cfg.InitialAdminPasswordFile()
	info, err := os.Stat(pwFile)
	if err != nil {
		t.Fatalf("未写入初始密码文件: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("密码文件权限应为 0600，实际 %o", perm)
	}
	raw, err := os.ReadFile(pwFile)
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		t.Fatal("密码文件为空")
	}
	// 文件里的密码必须真的能登录
	if !VerifyPassword(admin.PasswordHash, password) {
		t.Error("文件中的密码与哈希不匹配")
	}

	// 第二次：幂等，不得重复建号
	if err := Bootstrap(repo, cfg, log); err != nil {
		t.Fatalf("重复引导失败: %v", err)
	}
	n, err := repo.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("重复引导后用户数应为 1，实际 %d", n)
	}
}

func TestBootstrapSkipsWhenUsersExist(t *testing.T) {
	repo := newTestUserRepo(t)
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 先手工建一个普通用户（不是 admin@localhost）
	hash, err := HashPassword("whatever-123")
	if err != nil {
		t.Fatal(err)
	}
	now := model.Now()
	if err := repo.Create(&model.User{
		UID:          "usr_existing",
		Email:        "existing@example.com",
		PasswordHash: hash,
		Role:         model.UserRoleUser,
		Status:       model.UserStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}

	if err := Bootstrap(repo, cfg, log); err != nil {
		t.Fatalf("引导失败: %v", err)
	}

	n, err := repo.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("已有用户时不应再建管理员，用户数应为 1，实际 %d", n)
	}
	if _, err := repo.FindByEmail(BootstrapAdminEmail); err == nil {
		t.Error("已有用户时不应创建 admin@localhost")
	}
	// 也不应写密码文件
	if _, err := os.Stat(cfg.InitialAdminPasswordFile()); err == nil {
		t.Error("未建号时不应写初始密码文件")
	}
}
