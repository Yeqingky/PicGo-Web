package service

import (
	"context"
	"testing"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/scheduler"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// ---- 任务 ----

func TestJobListAndGet(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)

	f := e.writeTempFile("a.png", 10)
	res, err := e.upload.EnqueueBatch(context.Background(), alice, EnqueueBatchInput{Files: []IncomingFile{f}, StorageUID: storage.UID})
	wantOK(t, err)
	job := e.waitJob(res.JobUID, 10*time.Second)

	// 自己可见
	got, err := e.jobSvc.Get(job.UID, alice)
	wantOK(t, err)
	if got.UID != job.UID || len(got.Items) != 1 {
		t.Fatalf("详情不符：%+v", got)
	}

	// 他人不可见（非管理员）
	_, err = e.jobSvc.Get(job.UID, bob)
	wantCode(t, err, response.CodeForbidden)

	// 管理员可见
	_, err = e.jobSvc.Get(job.UID, adminViewer())
	wantOK(t, err)

	// 列表：普通用户只看自己的
	items, total, err := e.jobSvc.List(JobListInput{}, bob)
	wantOK(t, err)
	if total != 0 || len(items) != 0 {
		t.Fatalf("bob 不应看到 alice 的任务，实际 %d", total)
	}

	// bob 传 Scope=all 被静默降级
	items, total, err = e.jobSvc.List(JobListInput{Scope: "all"}, bob)
	wantOK(t, err)
	if total != 0 {
		t.Fatalf("普通用户 Scope=all 应降级，实际 %d", total)
	}

	// 管理员 Scope=all 能看到
	_, total, err = e.jobSvc.List(JobListInput{Scope: "all"}, adminViewer())
	wantOK(t, err)
	if total != 1 {
		t.Fatalf("管理员应看到 1 个任务，实际 %d", total)
	}
}

func TestJobDeleteOnlyFinished(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	// 运行中的任务不能删
	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_running", Kind: model.JobKindUpload, Status: model.JobStatusRunning,
		UserUID: "usr_admin", TotalItems: 1, CreatedAt: now,
	}, []model.JobItem{{JobUID: "job_running", Seq: 1, Status: model.JobStatusRunning}}))

	err := e.jobSvc.Delete(context.Background(), "job_running", adminViewer(), "", "")
	wantCode(t, err, response.CodeConflict)

	// 已结束的可以删
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_done", Kind: model.JobKindUpload, Status: model.JobStatusSucceeded,
		UserUID: "usr_admin", TotalItems: 1, CreatedAt: now,
	}, []model.JobItem{{JobUID: "job_done", Seq: 1, Status: model.JobStatusSucceeded}}))

	wantOK(t, e.jobSvc.Delete(context.Background(), "job_done", adminViewer(), "", ""))
	if _, err := e.jobs.FindByUID("job_done"); !isNotFound(err) {
		t.Fatalf("任务应已被删除：%v", err)
	}
	// 子项也应删除
	items, err := e.jobs.ListItems("job_done")
	wantOK(t, err)
	if len(items) != 0 {
		t.Fatalf("子项应级联删除，实际 %d", len(items))
	}
}

func TestJobLogsIncremental(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_logs", Kind: model.JobKindPluginInstall, Status: model.JobStatusRunning,
		UserUID: "usr_admin", CreatedAt: now,
	}, nil))

	for _, line := range []string{"[npm] start", "[npm] added 12 packages", "[npm] done"} {
		_, err := e.jobs.AppendLog("job_logs", line)
		wantOK(t, err)
	}

	// 全量
	res, err := e.jobSvc.Logs("job_logs", 0, 0, adminViewer())
	wantOK(t, err)
	if len(res.Items) != 3 || res.LastSeq != 3 {
		t.Fatalf("日志拉取不符：%+v", res)
	}

	// 增量：只看 Seq > 1
	res, err = e.jobSvc.Logs("job_logs", 1, 0, adminViewer())
	wantOK(t, err)
	if len(res.Items) != 2 || res.Items[0].Seq != 2 {
		t.Fatalf("增量拉取不符：%+v", res)
	}

	// HasMore
	for i := 0; i < 10; i++ {
		_, err := e.jobs.AppendLog("job_logs", "extra")
		wantOK(t, err)
	}
	res, err = e.jobSvc.Logs("job_logs", 0, 5, adminViewer())
	wantOK(t, err)
	if len(res.Items) != 5 || !res.HasMore {
		t.Fatalf("应返回 5 条且 HasMore=true：len=%d hasMore=%v", len(res.Items), res.HasMore)
	}
}

// ---- 操作日志 ----

func TestLogServiceFilters(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	uid := e.uploadOne(alice, "a.png", 10, storage.UID)
	_, err := e.gallery.Delete(context.Background(), uid, false, alice, "", "")
	wantOK(t, err)

	// 按类型过滤
	logs, total, err := e.logSvc.List(LogListInput{Types: []string{model.LogTypeImageDelete}})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("应有删除日志")
	}
	for _, l := range logs {
		if l.Type != model.LogTypeImageDelete {
			t.Fatalf("类型过滤失效：%s", l.Type)
		}
	}

	// 按用户过滤
	_, total, err = e.logSvc.List(LogListInput{UserUID: alice.UID})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("按用户过滤应能查到")
	}
	_, total, err = e.logSvc.List(LogListInput{UserUID: "usr_nobody"})
	wantOK(t, err)
	if total != 0 {
		t.Fatalf("不存在的用户应查不到，实际 %d", total)
	}

	// 关键词过滤（匹配 Detail 里的文件名）
	_, total, err = e.logSvc.List(LogListInput{Keyword: "a.png"})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("关键词应能匹配 Detail")
	}

	// 状态过滤
	_, total, err = e.logSvc.List(LogListInput{Status: model.LogStatusSuccess})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("成功状态应能查到")
	}

	// 详情与类型清单
	logs, _, err = e.logSvc.List(LogListInput{Types: []string{model.LogTypeImageDelete}})
	wantOK(t, err)
	view, err := e.logSvc.Get(logs[0].UID)
	wantOK(t, err)
	if view.Type != model.LogTypeImageDelete {
		t.Fatalf("详情类型不符：%s", view.Type)
	}
	if len(e.logSvc.Types()) == 0 {
		t.Fatal("类型清单不应为空")
	}

	// 不存在的日志
	_, err = e.logSvc.Get("log_nope")
	wantCode(t, err, response.CodeNotFound)
}

// ---- 邮件 ----

func TestEmailDisabledByDefault(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	if e.emailSvc.EmailEnabled() {
		t.Fatal("邮件默认应为未启用")
	}

	err := e.emailSvc.SendTest(context.Background(), "a@b.c", "usr_admin", "", "")
	wantCode(t, err, response.CodeInternal)
}

// TestForgotPasswordNoAccountEnumeration 断言**防账号枚举**（D29 / OPERATIONS §6）。
func TestForgotPasswordNoAccountEnumeration(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	// 邮件功能未启用时，两种情况都返回同一个错误（配置问题，必须让用户知道）
	err := e.emailSvc.RequestPasswordReset(context.Background(), "alice@example.com", "", "")
	wantCode(t, err, response.CodeInternal)
	err = e.emailSvc.RequestPasswordReset(context.Background(), "nobody@example.com", "", "")
	wantCode(t, err, response.CodeInternal)

	// 启用邮件后：**两种情况都返回 nil**
	wantOK(t, e.settings.Set("mail.enabled", true, "usr_admin"))
	wantOK(t, e.settings.Set("mail.host", "smtp.example.com", "usr_admin"))
	wantOK(t, e.settings.Set("mail.fromAddress", "noreply@example.com", "usr_admin"))

	// ⚠️ SMTP 连不上，但**不得**把「邮箱不存在」与「发送失败」区分开
	err = e.emailSvc.RequestPasswordReset(context.Background(), "alice@example.com", "", "")
	if err != nil {
		t.Fatalf("已启用邮件时，找回密码应静默返回（发信失败也不能泄露邮箱是否存在），实际 %v", err)
	}
	err = e.emailSvc.RequestPasswordReset(context.Background(), "nobody@example.com", "", "")
	if err != nil {
		t.Fatalf("不存在的邮箱也应静默返回，实际 %v", err)
	}
}

// TestEmailTemplatesRender 断言模板渲染（含中文与占位符）。
func TestEmailTemplatesRender(t *testing.T) {
	e := newW5Env(t)

	for _, name := range []string{
		model.MailTemplateTest,
		model.MailTemplateResetPassword,
		model.MailTemplateInvite,
	} {
		subject, body, err := e.emailSvc.render(name, map[string]any{
			"Token": "tok123", "TTLMinutes": 30, "Time": "2026-01-01", "Inviter": "admin",
		})
		wantOK(t, err)
		if subject == "" || body == "" {
			t.Fatalf("模板 %s 渲染为空", name)
		}
		if !containsStr(body, "PicGo Web") {
			t.Fatalf("模板 %s 应含站点名：%s", name, body)
		}
	}

	// 未知模板 → 40001
	_, _, err := e.emailSvc.render("no-such-template", nil)
	wantCode(t, err, response.CodeInvalidParam)
}

// ---- 定时清理（D74）----

func TestSchedulerPurgesExpiredLogs(t *testing.T) {
	e := newW5Env(t)

	// 造一条「很久以前」的操作日志与任务日志
	old := model.Now() - 400*86400 // 400 天前
	if err := e.db.Exec(
		`INSERT INTO "OperationLogs" ("UID","Type","Status","CreatedAt") VALUES (?,?,?,?)`,
		"log_old", model.LogTypeUpload, model.LogStatusSuccess, old,
	).Error; err != nil {
		t.Fatalf("插入旧日志失败: %v", err)
	}

	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_old", Kind: model.JobKindUpload, Status: model.JobStatusSucceeded,
		CreatedAt: now,
	}, nil))
	if err := e.db.Exec(
		`INSERT INTO "JobLogs" ("JobUID","Seq","Line","CreatedAt") VALUES (?,?,?,?)`,
		"job_old", 1, "old line", old,
	).Error; err != nil {
		t.Fatalf("插入旧任务日志失败: %v", err)
	}

	sched := scheduler.New(scheduler.Config{
		Log:      e.log,
		Settings: e.settings,
		Logs:     e.logs,
		Jobs:     e.jobs,
		Audit:    NewCleanupAuditWriter(e.audit),
	})

	res := sched.RunOnce(context.Background())
	if res.OperationLogsDeleted == 0 {
		t.Fatalf("应删除超期操作日志：%+v", res)
	}
	if res.JobLogsDeleted == 0 {
		t.Fatalf("应删除超期任务日志：%+v", res)
	}

	// 清理动作**自身也要留痕**（docs/OPERATIONS.md §5）
	_, total, err := e.logSvc.List(LogListInput{Types: []string{model.LogTypeLogCleanup}})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("清理动作应写一条 system.log.cleanup 日志")
	}
}

// TestSchedulerSkipsWhenZeroRetention 断言 `0` = 永久保留 → 跳过。
func TestSchedulerSkipsWhenZeroRetention(t *testing.T) {
	e := newW5Env(t)

	wantOK(t, e.settings.Set("log.retentionDays", 0, "usr_admin"))
	wantOK(t, e.settings.Set("log.jobRetentionDays", 0, "usr_admin"))

	// 造一条很老的日志，验证它**不会**被删
	old := model.Now() - 4000*86400
	if err := e.db.Exec(
		`INSERT INTO "OperationLogs" ("UID","Type","Status","CreatedAt") VALUES (?,?,?,?)`,
		"log_forever", model.LogTypeUpload, model.LogStatusSuccess, old,
	).Error; err != nil {
		t.Fatalf("插入日志失败: %v", err)
	}

	sched := scheduler.New(scheduler.Config{
		Log: e.log, Settings: e.settings, Logs: e.logs, Jobs: e.jobs,
		Audit: NewCleanupAuditWriter(e.audit),
	})
	res := sched.RunOnce(context.Background())

	if !res.SkippedOperationLogs || !res.SkippedJobLogs {
		t.Fatalf("retention=0 时应跳过：%+v", res)
	}
	if res.OperationLogsDeleted != 0 || res.JobLogsDeleted != 0 {
		t.Fatalf("retention=0 时不应删除任何东西：%+v", res)
	}

	// 那条老日志仍在
	if _, err := e.logSvc.Get("log_forever"); err != nil {
		t.Fatalf("永久保留的日志不应被删除：%v", err)
	}
}

func TestSchedulerDescription(t *testing.T) {
	e := newW5Env(t)
	sched := scheduler.New(scheduler.Config{
		Log: e.log, Settings: e.settings, Logs: e.logs, Jobs: e.jobs,
		Audit: NewCleanupAuditWriter(e.audit),
	})
	desc := sched.Description()
	if !containsStr(desc, "180") || !containsStr(desc, "7") {
		t.Fatalf("描述应含默认保留天数，实际 %q", desc)
	}
}

// 保证 settings 包被引用（本文件用到了它的类型常量）。
var _ = settings.SourceDB
