package service

import (
	"context"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

func TestIssueCreatesRefreshTokenRow(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "issue@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	issued, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest-agent")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if issued.AccessToken == "" || issued.RefreshToken == "" {
		t.Fatal("令牌不应为空")
	}
	if issued.ExpiresIn != int64(env.tokenSvc.AccessTTL().Seconds()) {
		t.Errorf("ExpiresIn 与 AccessTTL 不一致: %d", issued.ExpiresIn)
	}

	// 库里只应有哈希，绝不能有明文
	row, err := env.tokens.FindRefreshByHash(auth.HashToken(issued.RefreshToken))
	if err != nil {
		t.Fatalf("未找到 refresh token 行: %v", err)
	}
	if row.UserUID != u.UID {
		t.Errorf("归属用户不对: %s", row.UserUID)
	}
	if row.TokenHash == issued.RefreshToken {
		t.Error("数据库里存了明文令牌")
	}
	if row.RevokedAt != 0 {
		t.Error("新签发的会话不应是已吊销状态")
	}

	// access token 可校验
	claims, err := env.jwt.Verify(issued.AccessToken)
	if err != nil {
		t.Fatalf("access token 校验失败: %v", err)
	}
	if claims.Subject != u.UID || claims.Role != u.Role {
		t.Errorf("claim 不正确: sub=%s role=%s", claims.Subject, claims.Role)
	}
}

// TestRefreshRotation 刷新必须**轮换**：旧 refresh token 立即失效（D30，防重放）。
func TestRefreshRotation(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "rotate@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	first, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	second, refreshed, err := env.tokenSvc.Refresh(context.Background(), first.RefreshToken, testIP, "pytest")
	if err != nil {
		t.Fatalf("刷新失败: %v", err)
	}
	if refreshed.UID != u.UID {
		t.Errorf("刷新返回的用户不对: %s", refreshed.UID)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("刷新后应得到新的 refresh token")
	}

	// 旧 refresh token 已吊销 → 不能再用
	if _, _, err := env.tokenSvc.Refresh(context.Background(), first.RefreshToken, testIP, "pytest"); err == nil {
		t.Error("旧 refresh token 应已失效")
	} else {
		assertCode(t, err, response.CodeTokenExpired)
	}

	// 新的还能用
	if _, _, err := env.tokenSvc.Refresh(context.Background(), second.RefreshToken, testIP, "pytest"); err != nil {
		t.Errorf("新 refresh token 应可用: %v", err)
	}
}

func TestRefreshRejectsUnknownAndEmpty(t *testing.T) {
	env := newTestEnv(t)

	_, _, err := env.tokenSvc.Refresh(context.Background(), "", testIP, "pytest")
	assertCode(t, err, response.CodeUnauthorized)

	_, _, err = env.tokenSvc.Refresh(context.Background(), "not-a-real-token", testIP, "pytest")
	assertCode(t, err, response.CodeUnauthorized)
}

func TestRefreshRejectsDisabledAccount(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "willdisable@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	issued, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	// 禁用账号后刷新应被拒绝
	if err := env.users.UpdateFields(u.UID, map[string]any{"Status": model.UserStatusDisabled}); err != nil {
		t.Fatal(err)
	}
	_, _, err = env.tokenSvc.Refresh(context.Background(), issued.RefreshToken, testIP, "pytest")
	assertCode(t, err, response.CodeAccountDisabled)
}

func TestLogoutRevokesAndIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "logout@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	issued, err := env.tokenSvc.Issue(context.Background(), u, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	env.tokenSvc.Logout(context.Background(), issued.RefreshToken, testIP, "pytest")

	// 吊销后不能用
	if _, _, err := env.tokenSvc.Refresh(context.Background(), issued.RefreshToken, testIP, "pytest"); err == nil {
		t.Error("登出后 refresh token 应失效")
	}
	if n := env.logCount(t, model.LogTypeAuthLogout); n != 1 {
		t.Errorf("auth.logout 日志应为 1 条，实际 %d", n)
	}

	// 幂等：重复登出/空令牌都不应报错或 panic
	env.tokenSvc.Logout(context.Background(), issued.RefreshToken, testIP, "pytest")
	env.tokenSvc.Logout(context.Background(), "", testIP, "pytest")
	env.tokenSvc.Logout(context.Background(), "garbage", testIP, "pytest")
}

func TestRevokeAllForUser(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "revokeall@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)

	a, err := env.tokenSvc.Issue(context.Background(), u, testIP, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.tokenSvc.Issue(context.Background(), u, testIP, "device-b")
	if err != nil {
		t.Fatal(err)
	}

	if err := env.tokenSvc.RevokeAllForUser(u.UID); err != nil {
		t.Fatalf("吊销全部会话失败: %v", err)
	}

	for name, token := range map[string]string{"a": a.RefreshToken, "b": b.RefreshToken} {
		if _, _, err := env.tokenSvc.Refresh(context.Background(), token, testIP, "pytest"); err == nil {
			t.Errorf("会话 %s 应已被吊销", name)
		}
	}
}

// ---- API Token ----

func TestAPITokenLifecycle(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "api@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	created, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "CI 上传", 0, testIP, "pytest")
	if err != nil {
		t.Fatalf("创建令牌失败: %v", err)
	}
	if !auth.IsAPIToken(created.Token) {
		t.Errorf("令牌格式不对: %s", created.Token)
	}
	if created.ExpiresAt != 0 {
		t.Errorf("ExpiresInDays=0 应表示永不过期，实际 ExpiresAt=%d", created.ExpiresAt)
	}

	// 库里只存哈希
	row, err := env.tokens.FindAPITokenByHash(auth.HashToken(created.Token))
	if err != nil {
		t.Fatalf("未找到令牌行: %v", err)
	}
	if row.TokenHash == created.Token {
		t.Error("数据库里存了明文令牌")
	}
	if row.Prefix != created.Prefix {
		t.Errorf("前缀不一致: %s vs %s", row.Prefix, created.Prefix)
	}

	// 列表**不含明文**
	list, err := env.tokenSvc.ListAPITokens(u.UID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("列表应有 1 条，实际 %d", len(list))
	}
	if list[0].Name != "CI 上传" {
		t.Errorf("名称不一致: %s", list[0].Name)
	}

	// 删除后哈希查不到
	if err := env.tokenSvc.DeleteAPIToken(ctx, u.UID, created.UID, testIP, "pytest"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := env.tokens.FindAPITokenByHash(auth.HashToken(created.Token)); err == nil {
		t.Error("删除后不应还能查到令牌")
	}

	// 审计：创建与删除各一条 setting.update
	if n := env.logCount(t, model.LogTypeSettingUpdate); n != 2 {
		t.Errorf("setting.update 日志应为 2 条，实际 %d", n)
	}
}

func TestAPITokenRejectsDuplicateName(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "dup-token@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	if _, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "same-name", 0, testIP, "pytest"); err != nil {
		t.Fatal(err)
	}
	_, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "same-name", 0, testIP, "pytest")
	assertCode(t, err, response.CodeConflict)

	// 大小写不同也算重名
	_, err = env.tokenSvc.CreateAPIToken(ctx, u.UID, "SAME-NAME", 0, testIP, "pytest")
	assertCode(t, err, response.CodeConflict)
}

func TestAPITokenValidation(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "val-token@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	if _, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "  ", 0, testIP, "pytest"); err == nil {
		t.Error("空名称应报错")
	} else {
		assertCode(t, err, response.CodeInvalidParam)
	}

	long := ""
	for i := 0; i < 70; i++ {
		long += "a"
	}
	if _, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, long, 0, testIP, "pytest"); err == nil {
		t.Error("超长名称应报错")
	}
}

// TestAPITokenExpiry 过期令牌不能用于鉴权（由 Valid() 判定，这里直接验证语义）。
func TestAPITokenExpiry(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "exp@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	created, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "short-lived", 1, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}
	if created.ExpiresAt <= model.Now() {
		t.Error("1 天后过期的时间戳应在未来")
	}

	// 手工改成已过期
	if err := env.db.Model(&model.APIToken{}).
		Where(map[string]any{"UID": created.UID}).
		Updates(map[string]any{"ExpiresAt": model.Now() - 10}).Error; err != nil {
		t.Fatal(err)
	}

	row, err := env.tokens.FindAPITokenByHash(auth.HashToken(created.Token))
	if err != nil {
		t.Fatal(err)
	}
	if row.Valid() {
		t.Error("已过期的令牌 Valid() 应为 false")
	}
}

// TestAPITokenCannotBeDeletedByAnotherUser 越权防护：只能删自己的令牌。
func TestAPITokenCannotBeDeletedByAnotherUser(t *testing.T) {
	env := newTestEnv(t)
	owner := env.createUser(t, "owner@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	other := env.createUser(t, "other@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	created, err := env.tokenSvc.CreateAPIToken(ctx, owner.UID, "mine", 0, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	err = env.tokenSvc.DeleteAPIToken(ctx, other.UID, created.UID, testIP, "pytest")
	assertCode(t, err, response.CodeNotFound)

	// owner 的令牌仍在
	if _, err := env.tokens.FindAPITokenByHash(auth.HashToken(created.Token)); err != nil {
		t.Error("他人删除不应影响 owner 的令牌")
	}

	// 删除不存在的令牌
	err = env.tokenSvc.DeleteAPIToken(ctx, owner.UID, "tk_nonexistent", testIP, "pytest")
	assertCode(t, err, response.CodeNotFound)

	// 空标识
	err = env.tokenSvc.DeleteAPIToken(ctx, owner.UID, "", testIP, "pytest")
	assertCode(t, err, response.CodeInvalidParam)
}

// TestAPITokenPlaintextOnlyReturnedOnce 明文只在创建响应里出现（D31）。
func TestAPITokenPlaintextOnlyReturnedOnce(t *testing.T) {
	env := newTestEnv(t)
	u := env.createUser(t, "once@example.com", "pw-12345678", model.UserRoleUser, model.UserStatusActive)
	ctx := context.Background()

	created, err := env.tokenSvc.CreateAPIToken(ctx, u.UID, "once", 0, testIP, "pytest")
	if err != nil {
		t.Fatal(err)
	}

	// 再次列出：APITokenView 结构里根本没有 Token 字段
	list, err := env.tokenSvc.ListAPITokens(u.UID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.UID == created.UID {
			// 只有 Prefix 可用于展示
			if item.Prefix == "" {
				t.Error("应返回 Prefix 以便展示")
			}
			// 编译期保证：APITokenView 无 Token 字段
			_ = item
		}
	}

	// 审计里也不能出现明文
	var logs []model.OperationLog
	if err := env.db.Where(map[string]any{"Type": model.LogTypeSettingUpdate}).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	for _, l := range logs {
		if l.Detail != "" && contains(l.Detail, created.Token) {
			t.Error("审计日志里泄露了令牌明文")
		}
	}
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
