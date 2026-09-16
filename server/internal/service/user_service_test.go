package service

import (
	"context"
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

const testIP = "203.0.113.9"

func TestLoginSuccess(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "a@example.com", "correct-password", model.UserRoleUser, model.UserStatusActive)

	res, err := env.userSvc.Login(context.Background(), "a@example.com", "correct-password", testIP, "pytest")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if res.User.UID != u.UID {
		t.Errorf("返回的用户不对: %s", res.User.UID)
	}
	if res.User.LastLoginAt == 0 {
		t.Error("应更新 LastLoginAt")
	}

	// 审计：成功登录写 auth.login
	if n := env.logCount(t, model.LogTypeAuthLogin); n != 1 {
		t.Errorf("auth.login 日志应为 1 条，实际 %d", n)
	}

	// 登录尝试：成功也写一条
	var attempts int64
	if err := env.db.Model(&model.LoginAttempt{}).Count(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Errorf("登录尝试应记录 1 条，实际 %d", attempts)
	}
}

// TestLoginAntiEnumeration 防账号枚举（D23 相关约定）：
// 「邮箱不存在」与「密码错误」必须返回**同一个错误码**，且都不泄露原因。
func TestLoginAntiEnumeration(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "exists@example.com", "right-password", model.UserRoleUser, model.UserStatusActive)

	_, errUnknown := env.userSvc.Login(context.Background(), "nobody@example.com", "some-password", testIP, "pytest")
	_, errWrong := env.userSvc.Login(context.Background(), "exists@example.com", "wrong-password", testIP, "pytest")

	assertCode(t, errUnknown, response.CodeBadCredentials)
	assertCode(t, errWrong, response.CodeBadCredentials)

	// 消息也必须一致（不能一个说「邮箱不存在」一个说「密码错误」）
	if MessageOf(errUnknown) != MessageOf(errWrong) {
		t.Errorf("两种失败的提示文案不一致：%q vs %q", MessageOf(errUnknown), MessageOf(errWrong))
	}
	// 且不能透露「账号是否存在」或「到底哪一项错了」
	msg := MessageOf(errUnknown)
	for _, leak := range []string{"不存在", "未注册", "没有该", "密码不正确"} {
		if strings.Contains(msg, leak) {
			t.Errorf("提示泄露了失败原因：%q", msg)
		}
	}

	// 凭据错误不再写 OperationLogs（防刷爆）；限流计数仍在 LoginAttempts
	if n := env.logCount(t, model.LogTypeAuthFailed); n != 0 {
		t.Errorf("凭据错误不应写 auth.failed 日志，实际 %d 条", n)
	}
}

// TestLoginOAuthOnlyUserCannotUsePassword 纯 OAuth 用户不能走密码登录。
func TestLoginOAuthOnlyUserCannotUsePassword(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "oauth@example.com", "", model.UserRoleUser, model.UserStatusActive)

	_, err := env.userSvc.Login(context.Background(), "oauth@example.com", "any-password", testIP, "pytest")
	assertCode(t, err, response.CodeBadCredentials)
}

func TestLoginDisabledAccount(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "disabled@example.com", "correct-password", model.UserRoleUser, model.UserStatusDisabled)

	_, err := env.userSvc.Login(context.Background(), "disabled@example.com", "correct-password", testIP, "pytest")
	assertCode(t, err, response.CodeAccountDisabled)
}

// TestLoginRateLimit 登录限流（D30）：窗口内失败达到阈值后返回 42901。
func TestLoginRateLimit(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "limit@example.com", "correct-password", model.UserRoleUser, model.UserStatusActive)

	// 把阈值降到 3，避免测试跑 5 轮 bcrypt
	if err := env.settings.Set("security.loginMaxAttempts", int64(3), ""); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := env.userSvc.Login(ctx, "limit@example.com", "wrong", testIP, "pytest")
		assertCode(t, err, response.CodeBadCredentials)
	}

	// 第 4 次：即使密码正确也应被限流
	_, err := env.userSvc.Login(ctx, "limit@example.com", "correct-password", testIP, "pytest")
	assertCode(t, err, response.CodeTooMany)

	// 被限流的请求不应写入 LoginAttempts，否则攻击者持续重试会让账号被永久锁死
	var attempts int64
	if err := env.db.Model(&model.LoginAttempt{}).Count(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Errorf("被限流的请求不应写入 LoginAttempts：期望 3 条，实际 %d", attempts)
	}

	// 凭据错误与限流拒绝都不再写 OperationLogs（防刷爆）；
	// 限流依据只看 LoginAttempts（上面已断言）
	if n := env.logCount(t, model.LogTypeAuthFailed); n != 0 {
		t.Errorf("登录失败不应写 auth.failed 日志，实际 %d 条", n)
	}

	// 换一个 IP 不受影响（限流按 Email + IP）
	_, err = env.userSvc.Login(ctx, "limit@example.com", "correct-password", "198.51.100.7", "pytest")
	if err != nil {
		t.Errorf("换 IP 后应可登录，实际 %v", err)
	}
}

// TestLoginSuccessClearsFailures 登录成功后清空失败计数，正常用户不必等窗口过期。
func TestLoginSuccessClearsFailures(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "clear@example.com", "correct-password", model.UserRoleUser, model.UserStatusActive)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = env.userSvc.Login(ctx, "clear@example.com", "wrong", testIP, "pytest")
	}
	if _, err := env.userSvc.Login(ctx, "clear@example.com", "correct-password", testIP, "pytest"); err != nil {
		t.Fatalf("第 3 次（正确密码）应成功: %v", err)
	}

	failures, err := env.attempts.CountFailuresSince("clear@example.com", testIP, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Errorf("登录成功后失败计数应清零，实际 %d", failures)
	}
}

// TestLoginEmailNormalization 邮箱大小写/空白归一化，避免出现两个「同一邮箱」的账号。
func TestLoginEmailNormalization(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "mixed@example.com", "correct-password", model.UserRoleUser, model.UserStatusActive)

	if _, err := env.userSvc.Login(context.Background(), "  MiXeD@Example.COM  ", "correct-password", testIP, "pytest"); err != nil {
		t.Errorf("大小写/空白不同的邮箱也应能登录: %v", err)
	}
}

// ---- 创建用户 ----

func TestCreateUserUsesDefaultCapacity(t *testing.T) {
	env := newTestEnv(t)

	view, err := env.userSvc.Create(context.Background(), CreateUserInput{
		Email:    "new@example.com",
		Password: "strong-password",
	}, "", testIP, "pytest")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// 默认 user.defaultCapacityBytes = 5 GiB
	if view.CapacityBytes != 5<<30 {
		t.Errorf("应使用默认配额 5GiB，实际 %d", view.CapacityBytes)
	}
	if view.Role != model.UserRoleUser {
		t.Errorf("默认角色应为 user，实际 %s", view.Role)
	}
	if view.Status != model.UserStatusActive {
		t.Errorf("默认状态应为 active，实际 %s", view.Status)
	}
	if view.Nickname != "new" {
		t.Errorf("昵称应回退为邮箱前缀 new，实际 %q", view.Nickname)
	}
	if !view.HasPassword {
		t.Error("应标记为已设置密码")
	}
	if n := env.logCount(t, model.LogTypeUserCreate); n != 1 {
		t.Errorf("user.create 日志应为 1 条，实际 %d", n)
	}
}

func TestCreateUserExplicitCapacityWins(t *testing.T) {
	env := newTestEnv(t)
	want := int64(1234567)
	view, err := env.userSvc.Create(context.Background(), CreateUserInput{
		Email: "cap@example.com", Password: "strong-password", CapacityBytes: &want,
	}, "", testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}
	if view.CapacityBytes != want {
		t.Errorf("显式配额应生效：期望 %d，实际 %d", want, view.CapacityBytes)
	}
}

func TestCreateUserUnlimitedCapacity(t *testing.T) {
	env := newTestEnv(t)
	if err := env.settings.Set("user.unlimitedCapacity", true, ""); err != nil {
		t.Fatal(err)
	}
	view, err := env.userSvc.Create(context.Background(), CreateUserInput{
		Email: "unlimited@example.com", Password: "strong-password",
	}, "", testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}
	if view.CapacityBytes != 0 {
		t.Errorf("user.unlimitedCapacity=true 时应记 0（不限额），实际 %d", view.CapacityBytes)
	}
}

func TestCreateUserDuplicateEmail(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "dup@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	_, err := env.userSvc.Create(context.Background(), CreateUserInput{
		Email: "dup@example.com", Password: "strong-password",
	}, "", testIP, "pytest")
	assertCode(t, err, response.CodeConflict)

	// 大小写不同也算重复
	_, err = env.userSvc.Create(context.Background(), CreateUserInput{
		Email: "DUP@example.com", Password: "strong-password",
	}, "", testIP, "pytest")
	assertCode(t, err, response.CodeConflict)
}

func TestCreateUserValidation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreateUserInput
		code response.Code
	}{
		{"空邮箱", CreateUserInput{Email: "", Password: "strong-password"}, response.CodeInvalidParam},
		{"非法邮箱", CreateUserInput{Email: "not-an-email", Password: "strong-password"}, response.CodeInvalidParam},
		{"缺域名点", CreateUserInput{Email: "a@b", Password: "strong-password"}, response.CodeInvalidParam},
		{"含空格", CreateUserInput{Email: "a b@c.com", Password: "strong-password"}, response.CodeInvalidParam},
		{"密码过短", CreateUserInput{Email: "ok@example.com", Password: "short"}, response.CodeInvalidParam},
		{"密码过长", CreateUserInput{Email: "ok2@example.com", Password: strings.Repeat("x", 73)}, response.CodeInvalidParam},
		{"非法角色", CreateUserInput{Email: "ok3@example.com", Password: "strong-password", Role: "superuser"}, response.CodeInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.userSvc.Create(ctx, tc.in, "", testIP, "pytest")
			assertCode(t, err, tc.code)
		})
	}
}

// ---- 更新用户 ----

func TestUpdateCannotChangeOwnRoleOrStatus(t *testing.T) {
	env := newTestEnv(t)
	admin := env.createUser(t, "admin@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)

	role := model.UserRoleUser
	_, _, err := env.userSvc.Update(context.Background(), admin.UID, UpdateUserInput{Role: &role}, admin.UID, testIP, "pytest")
	assertCode(t, err, response.CodeForbidden)

	status := model.UserStatusDisabled
	_, _, err = env.userSvc.Update(context.Background(), admin.UID, UpdateUserInput{Status: &status}, admin.UID, testIP, "pytest")
	assertCode(t, err, response.CodeForbidden)
}

// TestUpdateCannotDemoteOrDisableLastAdmin 系统必须保留至少一个可用管理员。
func TestUpdateCannotDemoteOrDisableLastAdmin(t *testing.T) {
	env := newTestEnv(t)
	admin := env.createUser(t, "only-admin@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)
	other := env.createUser(t, "op@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	// 由另一个非 admin 操作者是没意义的（真实请求会被 RequireAdmin 拦住），
	// 这里直接测 service 规则：把最后一个 admin 降权/禁用必须被拒绝。
	role := model.UserRoleUser
	_, _, err := env.userSvc.Update(context.Background(), admin.UID, UpdateUserInput{Role: &role}, other.UID, testIP, "pytest")
	assertCode(t, err, response.CodeConflict)

	status := model.UserStatusDisabled
	_, _, err = env.userSvc.Update(context.Background(), admin.UID, UpdateUserInput{Status: &status}, other.UID, testIP, "pytest")
	assertCode(t, err, response.CodeConflict)
}

func TestUpdateAllowsDemotingAdminWhenAnotherActiveAdminExists(t *testing.T) {
	env := newTestEnv(t)
	a1 := env.createUser(t, "a1@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)
	env.createUser(t, "a2@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)

	role := model.UserRoleUser
	view, _, err := env.userSvc.Update(context.Background(), a1.UID, UpdateUserInput{Role: &role}, a1.UID+".operator", testIP, "pytest")
	if err != nil {
		t.Fatalf("有第二个管理员时应允许降权: %v", err)
	}
	if view.Role != model.UserRoleUser {
		t.Errorf("角色应已改为 user，实际 %s", view.Role)
	}
}

func TestUpdateCapacityBelowUsedReturnsHint(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "quota@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	// 造一条已用容量
	if err := env.users.UpdateFields(u.UID, map[string]any{"UsedBytes": int64(1000)}); err != nil {
		t.Fatal(err)
	}

	small := int64(100)
	view, hint, err := env.userSvc.Update(context.Background(), u.UID,
		UpdateUserInput{CapacityBytes: &small}, "op", testIP, "pytest")
	if err != nil {
		t.Fatalf("下调配额到低于已用应被允许（不追溯删图）: %v", err)
	}
	if view.CapacityBytes != small {
		t.Errorf("配额应已更新为 %d，实际 %d", small, view.CapacityBytes)
	}
	if hint == "" {
		t.Error("配额低于已用量时应返回提示")
	}
	if n := env.logCount(t, model.LogTypeUserUpdate); n != 1 {
		t.Errorf("user.update 日志应为 1 条，实际 %d", n)
	}
}

func TestUpdatePasswordResetsFlagsAndRevokesSessions(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "reset-target@example.com", "old-password", model.UserRoleUser, model.UserStatusActive)

	// 先签发一次会话，稍后断言它被吊销
	if _, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest"); err != nil {
		t.Fatal(err)
	}

	newPw := "brand-new-password"
	view, _, err := env.userSvc.Update(context.Background(), u.UID,
		UpdateUserInput{NewPassword: &newPw}, "op", testIP, "pytest")
	if err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if !view.MustChangePassword {
		t.Error("管理员重置密码后应强制对方首次登录改密")
	}

	// 会话应被吊销
	var revoked int64
	if err := env.db.Model(&model.RefreshToken{}).
		Where(map[string]any{"UserUID": u.UID}).
		Where(`"RevokedAt" > 0`).Count(&revoked).Error; err != nil {
		t.Fatal(err)
	}
	if revoked == 0 {
		t.Error("重置密码后应吊销该用户全部 refresh token")
	}

	// 新密码可用
	if _, err := env.userSvc.Login(context.Background(), "reset-target@example.com", newPw, testIP, "pytest"); err != nil {
		t.Errorf("新密码应能登录: %v", err)
	}
}

// ---- 删除用户 ----

func TestDeleteCannotDeleteSelf(t *testing.T) {
	env := newTestEnv(t)
	admin := env.createUser(t, "self@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)

	_, err := env.userSvc.Delete(context.Background(), admin.UID, admin.UID, testIP, "pytest")
	assertCode(t, err, response.CodeForbidden)
}

func TestDeleteCannotDeleteLastAdmin(t *testing.T) {
	env := newTestEnv(t)
	admin := env.createUser(t, "last-admin@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)

	_, err := env.userSvc.Delete(context.Background(), admin.UID, "some-operator", testIP, "pytest")
	assertCode(t, err, response.CodeConflict)
}

func TestDeletePurgesRelatedRows(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "keeper@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)
	target := env.createUser(t, "gone@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	// 造关联数据：Profile、绑定、refresh token、API token、相册、图片
	now := model.Now()
	if err := env.users.UpdateFields(target.UID, map[string]any{"UsedBytes": int64(2048)}); err != nil {
		t.Fatal(err)
	}
	if err := env.users.CreateIdentity(&model.OAuthIdentity{
		UID: "oid_x", UserUID: target.UID, Provider: model.ProviderGitHub,
		ProviderUserID: "42", ProviderLogin: "gh", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.tokenSvc.Issue(context.Background(), target, testIP, "pytest"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.tokenSvc.CreateAPIToken(context.Background(), target.UID, "ci", 0, testIP, "pytest"); err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&model.Upload{
		UID: "up_1", UserUID: target.UID, StorageUID: "st_1", FileName: "a.png",
		Size: 2048, Status: model.UploadStatusSuccess, Source: model.UploadSourceWeb,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&model.UploadResult{
		UploadUID: "up_1", RawOutput: `[{"fileName":"a.png"}]`, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := env.userSvc.Delete(context.Background(), target.UID, "operator", testIP, "pytest")
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if result.DeletedUploads != 1 {
		t.Errorf("应报告删除 1 张图片，实际 %d", result.DeletedUploads)
	}
	if result.FreedBytes != 2048 {
		t.Errorf("应报告释放 2048 字节，实际 %d", result.FreedBytes)
	}

	// 全部从属行都应清干净（不建外键，级联靠这里保证）
	checks := []struct {
		name  string
		model any
	}{
		{"UserProfiles", &model.UserProfile{}},
		{"OAuthIdentities", &model.OAuthIdentity{}},
		{"RefreshTokens", &model.RefreshToken{}},
		{"APITokens", &model.APIToken{}},
		{"Uploads", &model.Upload{}},
	}
	for _, c := range checks {
		var n int64
		if err := env.db.Model(c.model).Where(map[string]any{"UserUID": target.UID}).Count(&n).Error; err != nil {
			t.Fatalf("统计 %s 失败: %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("%s 应被清空，仍有 %d 行", c.name, n)
		}
	}

	// UploadResults 通过子查询清理，用 UploadUID 校验
	var ur int64
	if err := env.db.Model(&model.UploadResult{}).Where(map[string]any{"UploadUID": "up_1"}).Count(&ur).Error; err != nil {
		t.Fatal(err)
	}
	if ur != 0 {
		t.Errorf("UploadResults 应被清空，仍有 %d 行", ur)
	}

	// 用户本身
	if _, err := env.users.FindByUID(target.UID); err == nil {
		t.Error("用户记录应已删除")
	}
	// 保留 admin 不受影响
	if _, err := env.users.FindByEmail("keeper@example.com"); err != nil {
		t.Errorf("其他用户不应受影响: %v", err)
	}
}

// ---- 改密 / 重置 ----

func TestResetPassword(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "reset@example.com", "old-password", model.UserRoleUser, model.UserStatusActive)

	pw, err := env.userSvc.ResetPassword(context.Background(), u.UID, "op", testIP, "pytest")
	if err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	if len(pw) != 16 {
		t.Errorf("随机密码长度应为 16，实际 %d", len(pw))
	}

	got, err := env.users.FindByUID(u.UID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MustChangePassword {
		t.Error("重置后应强制首次改密")
	}
	if !auth.VerifyPassword(got.PasswordHash, pw) {
		t.Error("返回的密码应能通过校验")
	}
	if auth.VerifyPassword(got.PasswordHash, "old-password") {
		t.Error("旧密码应已失效")
	}
}

func TestChangePasswordRequiresOldPassword(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "chg@example.com", "old-password", model.UserRoleUser, model.UserStatusActive)

	// 非强制改密状态：必须提供旧密码
	err := env.userSvc.ChangePassword(context.Background(), u.UID, "", "new-password-1", testIP, "pytest")
	assertCode(t, err, response.CodeInvalidParam)

	// 旧密码错误
	err = env.userSvc.ChangePassword(context.Background(), u.UID, "wrong", "new-password-1", testIP, "pytest")
	assertCode(t, err, response.CodeBadCredentials)

	// 新密码相同
	err = env.userSvc.ChangePassword(context.Background(), u.UID, "old-password", "old-password", testIP, "pytest")
	assertCode(t, err, response.CodeInvalidParam)

	// 正常改密
	if err := env.userSvc.ChangePassword(context.Background(), u.UID, "old-password", "new-password-1", testIP, "pytest"); err != nil {
		t.Fatalf("改密失败: %v", err)
	}

	// 旧密码不可用，新密码可用
	if _, err := env.userSvc.Login(context.Background(), "chg@example.com", "old-password", testIP, "pytest"); err == nil {
		t.Error("旧密码不应还能登录")
	}
	if _, err := env.userSvc.Login(context.Background(), "chg@example.com", "new-password-1", testIP, "pytest"); err != nil {
		t.Errorf("新密码应能登录: %v", err)
	}
}

// TestChangePasswordForcedAllowsEmptyOld 强制改密场景（首启引导 D32）允许不填旧密码。
func TestChangePasswordForcedAllowsEmptyOld(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "forced@example.com", "initial-password", model.UserRoleUser, model.UserStatusActive)
	if err := env.users.UpdateFields(u.UID, map[string]any{"MustChangePassword": true}); err != nil {
		t.Fatal(err)
	}

	if err := env.userSvc.ChangePassword(context.Background(), u.UID, "", "brand-new-password", testIP, "pytest"); err != nil {
		t.Fatalf("强制改密时旧密码可省略: %v", err)
	}

	got, _ := env.users.FindByUID(u.UID)
	if got.MustChangePassword {
		t.Error("改密后 MustChangePassword 应置为 false")
	}
}

func TestChangePasswordRevokesRefreshTokens(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "revoke@example.com", "old-password", model.UserRoleUser, model.UserStatusActive)

	issued, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	if err := env.userSvc.ChangePassword(context.Background(), u.UID, "old-password", "new-password-9", testIP, "pytest"); err != nil {
		t.Fatal(err)
	}

	// 改密后旧 refresh token 必须失效（强制重新登录）
	if _, _, err := env.tokenSvc.Refresh(context.Background(), issued.RefreshToken, testIP, "pytest"); err == nil {
		t.Error("改密后旧 refresh token 应失效")
	}
}

// ---- 查询 ----

func TestListUsersFilterAndPaging(t *testing.T) {
	env := newTestEnv(t)
	env.createUser(t, "alice@example.com", "pw-12345678", model.UserRoleAdmin, model.UserStatusActive)
	env.createUser(t, "bob@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	env.createUser(t, "carol@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusDisabled)

	// 全量
	items, total, err := env.userSvc.List(repository.UserListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Errorf("应返回 3 个用户，实际 total=%d len=%d", total, len(items))
	}

	// 按角色
	items, total, err = env.userSvc.List(repository.UserListFilter{Role: model.UserRoleAdmin, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Email != "alice@example.com" {
		t.Errorf("按角色过滤失败: total=%d", total)
	}

	// 按状态
	_, total, err = env.userSvc.List(repository.UserListFilter{Status: model.UserStatusDisabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("按状态过滤失败: total=%d", total)
	}

	// 关键词匹配邮箱
	items, total, err = env.userSvc.List(repository.UserListFilter{Keyword: "bob", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Email != "bob@example.com" {
		t.Errorf("按关键词过滤失败: total=%d", total)
	}

	// 关键词匹配昵称（昵称在 UserProfiles，走子查询）
	items, total, err = env.userSvc.List(repository.UserListFilter{Keyword: "carol@", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("按昵称/邮箱模糊匹配失败: total=%d", total)
	}

	// 分页
	items, total, err = env.userSvc.List(repository.UserListFilter{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 1 {
		t.Errorf("分页失败: total=%d len=%d", total, len(items))
	}

	// PageSize 越界会被夹到上限
	_, _, err = env.userSvc.List(repository.UserListFilter{Page: 1, PageSize: 9999})
	if err != nil {
		t.Fatalf("超大 PageSize 不应报错: %v", err)
	}
}

func TestGetUserNotFound(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.userSvc.Get("usr_nonexistent")
	assertCode(t, err, response.CodeNotFound)
}
