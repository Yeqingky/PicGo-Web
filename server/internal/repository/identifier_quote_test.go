package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	gl "gorm.io/gorm/logger"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// sqlCapture 是只记录 SQL 的 GORM logger。
//
// 配合 DryRun 会话使用：GORM 的 processor.Execute 会**先**调用 Logger.Trace，
// **之后**才判断 DryRun 决定是否真的执行（gorm@v1.31 callbacks.go）。
// 因此既能拿到**真实仓储方法**生成的 SQL，又不会真的读写数据库。
type sqlCapture struct {
	mu   sync.Mutex
	sqls []string
}

func (c *sqlCapture) LogMode(gl.LogLevel) gl.Interface              { return c }
func (c *sqlCapture) Info(context.Context, string, ...interface{})  {}
func (c *sqlCapture) Warn(context.Context, string, ...interface{})  {}
func (c *sqlCapture) Error(context.Context, string, ...interface{}) {}
func (c *sqlCapture) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	c.mu.Lock()
	c.sqls = append(c.sqls, sql)
	c.mu.Unlock()
}

func (c *sqlCapture) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sqls
	c.sqls = nil
	return out
}

// captureOf 执行 fn 并返回期间产生的全部 SQL。
func captureOf(cap *sqlCapture, fn func()) []string {
	cap.take() // 丢弃之前的
	fn()
	return cap.take()
}

// TestRepositorySQLQuotesIdentifiers 守住「手工拼写的 SQL 标识符必须带双引号」这条约定（D81.4）。
//
// 为什么需要它：表名列名是 PascalCase，而 PostgreSQL 会把**未加引号**的标识符
// 折叠成小写 —— `ORDER BY CreatedAt` 会变成 `createdat` 并报 column does not exist。
// **SQLite 对标识符大小写不敏感，本地怎么测都测不出来**，只能检查生成出来的 SQL 字面量。
//
// 它同时守住 GORM 的一条**反直觉**行为：Group 的字符串会被 GORM 自己再加一层引号，
// 而 Order 的字符串是原样透传 —— 两者处理方式**相反**，很容易写错。
//
// 本测试**调用真实的仓储方法**（不是内联拼 SQL），因此能真正抓到生产代码里的写法问题。
func TestRepositorySQLQuotesIdentifiers(t *testing.T) {
	base := newTestRepo(t)

	cap := &sqlCapture{}

	// DryRun 会话：只构建 SQL 不执行。用于绝大多数只读查询。
	session := base.db.Session(&gorm.Session{DryRun: true, Logger: cap})
	users := NewUserRepo(session)
	tokens := NewTokenRepo(session)
	attempts := NewLoginAttemptRepo(session)

	// 真实执行 + 捕获：Purge 用了显式事务，而**事务在 DryRun 下会直接报
	// "dry run mode unsupported"**，所以只能真跑（在临时库上，无副作用）。
	realSession := base.db.Session(&gorm.Session{Logger: cap})
	realUsers := NewUserRepo(realSession)

	// 给 Purge 准备一个待删除用户及其从属数据
	seedVictim(t, base, realSession)

	t.Log("注：表名的引号由 GORM 按方言处理（SQLite 用反引号 / PgSQL 用双引号），" +
		"本测试只关心**手工拼写的列名**是否被正确加引号。")

	cases := []struct {
		name           string
		call           func()
		mustContain    []string
		mustNotContain []string
		why            string
	}{
		{
			name: "UserRepo.List（关键词 + 角色 + 排序）",
			call: func() {
				_, _, _ = users.List(UserListFilter{
					Keyword: "a", Role: model.UserRoleAdmin, Page: 1, PageSize: 20,
				})
			},
			mustContain: []string{
				`"Email" LIKE`,
				`"UID" IN (SELECT "UserUID" FROM "UserProfiles"`,
				`"Nickname" LIKE`,
				`ORDER BY "CreatedAt" DESC`,
			},
			mustNotContain: []string{`ORDER BY CreatedAt`},
			why:            "关键词条件与排序都是原样透传的原生片段，必须自己加引号",
		},
		{
			name: "UserRepo.List（按邮箱排序）",
			call: func() {
				_, _, _ = users.List(UserListFilter{Sort: "email", Order: "asc", Page: 1, PageSize: 20})
			},
			mustContain:    []string{`ORDER BY "Email" ASC`},
			mustNotContain: []string{`ORDER BY Email`},
			why:            "排序字段来自白名单，仍必须加引号",
		},
		{
			name: "UserRepo.ListIdentities",
			call: func() {
				_, _ = users.ListIdentities("usr_x")
			},
			mustContain:    []string{`ORDER BY "CreatedAt" ASC`},
			mustNotContain: []string{`ORDER BY CreatedAt`},
			why:            "Order 与 Group 相反：GORM 不加引号，必须自己加",
		},
		{
			name: "TokenRepo.ListAPITokens",
			call: func() {
				_, _ = tokens.ListAPITokens("usr_x")
			},
			mustContain:    []string{`ORDER BY "CreatedAt" DESC`},
			mustNotContain: []string{`ORDER BY CreatedAt`},
			why:            "同上",
		},
		{
			name: "TokenRepo.DeleteExpiredRefresh（原生 OR 条件）",
			call: func() {
				_, _ = tokens.DeleteExpiredRefresh(0)
			},
			mustContain:    []string{`"ExpiresAt" <`, `"RevokedAt" >`},
			mustNotContain: []string{`ExpiresAt <`, `RevokedAt >`},
			why:            "原生 WHERE 片段必须手工加引号",
		},
		{
			name: "LoginAttemptRepo.CountFailuresSince",
			call: func() {
				_, _ = attempts.CountFailuresSince("a@b.c", "127.0.0.1", 0)
			},
			mustContain:    []string{`"CreatedAt" >=`},
			mustNotContain: []string{`CreatedAt >=`},
			why:            "时间窗条件用的是原生片段",
		},
		{
			name: "LoginAttemptRepo.DeleteOlderThan",
			call: func() {
				_, _ = attempts.DeleteOlderThan(0)
			},
			mustContain:    []string{`"CreatedAt" <`},
			mustNotContain: []string{`CreatedAt <`},
			why:            "同上",
		},
		{
			name: "UserRepo.UploadStats（原生聚合）",
			call: func() {
				_, _, _ = users.UploadStats("usr_x")
			},
			mustContain: []string{
				`FROM "Uploads"`, `"UserUID" =`, `SUM("Size")`, `FROM "Uploads"`,
			},
			why: "Raw 完全原样执行，表名与列名都要自己加引号（SUM 空集靠 COALESCE 兜底）",
		},
		{
			name: "UserRepo.CountUploadsByUser（GROUP BY）",
			call: func() {
				_, _ = users.CountUploadsByUser([]string{"usr_x"})
			},
			// GORM 会给 Group 的字符串**再包一层引号**：SQLite 下是反引号、PgSQL 下是双引号。
			// 若手工加了双引号，会变成 ``GROUP BY `"UserUID"` `` → 报 no such column。
			mustContain:    []string{`"UserUID" IN`, `COUNT(*) AS "Cnt"`},
			mustNotContain: []string{`GROUP BY "UserUID"`},
			why:            "Group 与 Order 相反：必须传裸列名，否则引号会被当成标识符的一部分",
		},
		{
			name: "Purge（级联删除的原生 DELETE，真实执行 + 捕获）",
			call: func() {
				// 事务在 DryRun 下不可用，因此在临时库上真跑（无副作用）
				if _, err := realUsers.Purge(victimUID); err != nil {
					t.Errorf("Purge 执行失败: %v", err)
				}
			},
			mustContain: []string{
				`DELETE FROM "UploadResults" WHERE "UploadUID" IN (SELECT "UID" FROM "Uploads" WHERE "UserUID"`,
				`DELETE FROM "Uploads" WHERE "UserUID"`,
				`DELETE FROM "Users" WHERE "UID"`,
			},
			mustNotContain: []string{`WHERE UserUID`, `WHERE UID`},
			why:            "手写 DELETE 的表名与列名全部用 col() 包引号",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqls := captureOf(cap, tc.call)
			if len(sqls) == 0 {
				t.Fatalf("没有捕获到任何 SQL（DryRun + Logger.Trace 失效），测试会变成空转")
			}
			joined := strings.Join(sqls, "\n")

			for _, want := range tc.mustContain {
				if !strings.Contains(joined, want) {
					t.Errorf("SQL 缺少 %q（%s）\n--- 实际 SQL ---\n%s", want, tc.why, joined)
				}
			}
			for _, bad := range tc.mustNotContain {
				if strings.Contains(joined, bad) {
					t.Errorf("SQL 含不应出现的 %q（%s）\n--- 实际 SQL ---\n%s", bad, tc.why, joined)
				}
			}
		})
	}
}

// victimUID 是给 Purge 用例准备的待删除用户。
const victimUID = "usr_victim"

// seedVictim 造出一个带从属数据的用户，让 Purge 的每条 DELETE 都真的有事可做。
func seedVictim(t *testing.T, base *UserRepo, exec *gorm.DB) {
	t.Helper()

	now := time.Now().Unix()
	if err := exec.Create(&model.User{
		UID: victimUID, Email: "victim@example.com", Role: model.UserRoleUser,
		Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}
	if err := exec.Create(&model.UserProfile{
		UserUID: victimUID, Nickname: "victim", Locale: "zh-CN", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("预置资料失败: %v", err)
	}
	if err := exec.Create(&model.Upload{
		UID: "up_victim", UserUID: victimUID, StorageUID: "st_x", FileName: "v.png",
		Size: 128, Status: model.UploadStatusSuccess, Source: model.UploadSourceWeb,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("预置图片失败: %v", err)
	}
	if err := exec.Create(&model.UploadResult{
		UploadUID: "up_victim", RawOutput: `[{"fileName":"v.png"}]`, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("预置上传结果失败: %v", err)
	}
}
