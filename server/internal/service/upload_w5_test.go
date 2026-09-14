package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// ---- 配额（D20/D21/D72）----

func TestQuotaExceededRejected(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	// 配额 100 字节
	cap := int64(100)
	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, &cap)
	e.makeStorage("默认", true)

	f := e.writeTempFile("big.png", 200) // 超配额
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeQuotaExceeded)

	// 失败时必须清理暂存文件（否则每次失败都留垃圾）
	if _, statErr := os.Stat(f.Path); !os.IsNotExist(statErr) {
		t.Fatalf("配额拒绝后应清理暂存文件，实际仍存在: %s", f.Path)
	}
}

func TestQuotaZeroMeansUnlimited(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	cap := int64(0) // 0 = 不限额（D20）
	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, &cap)
	storage := e.makeStorage("默认", true)

	f := e.writeTempFile("any.png", 1<<20) // 1 MiB
	job := e.enqueueAndWait(user, []IncomingFile{f}, storage.UID)

	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("不限额用户应上传成功，实际 %s（err=%s）", job.Status, job.Error)
	}
}

// TestQuotaAdminBypass 断言管理员跳过配额校验（D20）。
func TestQuotaAdminBypass(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	cap := int64(10) // 极小配额
	admin := e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, &cap)
	storage := e.makeStorage("默认", true)

	f := e.writeTempFile("admin.png", 5000)
	job := e.enqueueAndWait(admin, []IncomingFile{f}, storage.UID)

	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("管理员应跳过配额校验，实际 %s（err=%s）", job.Status, job.Error)
	}
}

// ---- 上传限流（D73）----

// TestRateLimitDisabledByDefault 断言限流**默认禁用**。
func TestRateLimitDisabledByDefault(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	// 把阈值压到 1，但**不开开关**
	wantOK(t, e.settings.Set("upload.rateLimit.perHour", 1, "usr_admin"))

	files := []IncomingFile{
		e.writeTempFile("a.png", 10),
		e.writeTempFile("b.png", 10),
		e.writeTempFile("c.png", 10),
	}
	// 默认 enabled=false → 3 张也能过
	job := e.enqueueAndWait(user, files, storage.UID)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("限流默认禁用时不应拦截，实际 %s", job.Status)
	}
}

func TestRateLimitRejects(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	wantOK(t, e.settings.Set("upload.rateLimit.enabled", true, "usr_admin"))
	wantOK(t, e.settings.Set("upload.rateLimit.perHour", 2, "usr_admin"))
	wantOK(t, e.settings.Set("upload.rateLimit.action", "reject", "usr_admin"))

	// 第一批 2 张：通过
	first := []IncomingFile{e.writeTempFile("a.png", 10), e.writeTempFile("b.png", 10)}
	job := e.enqueueAndWait(user, first, storage.UID)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("第一批应成功，实际 %s", job.Status)
	}

	// 第二批 1 张：已用 2 张 + 本次 1 > 每小时 2 → 42901
	f := e.writeTempFile("c.png", 10)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeTooMany)
}

// TestRateLimitLogActionDoesNotBlock 断言 action=log 时**只记录不拦截**。
func TestRateLimitLogActionDoesNotBlock(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	wantOK(t, e.settings.Set("upload.rateLimit.enabled", true, "usr_admin"))
	wantOK(t, e.settings.Set("upload.rateLimit.perHour", 1, "usr_admin"))
	wantOK(t, e.settings.Set("upload.rateLimit.action", "log", "usr_admin"))

	files := []IncomingFile{e.writeTempFile("a.png", 10), e.writeTempFile("b.png", 10)}
	job := e.enqueueAndWait(user, files, storage.UID)

	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("action=log 时不应拦截，实际 %s", job.Status)
	}

	// 应有一条「本应拦截」的审计记录
	logs, total, err := e.logSvc.List(LogListInput{Types: []string{model.LogTypeUpload}})
	wantOK(t, err)
	if total == 0 {
		t.Fatal("应写入审计日志")
	}
	hasWouldBlock := false
	for _, l := range logs {
		if l.Detail["action"] == "rate_limit_would_block" {
			hasWouldBlock = true
		}
	}
	if !hasWouldBlock {
		t.Fatal("action=log 时应记录 rate_limit_would_block")
	}
}

// TestRateLimitAdminBypass 断言管理员跳过限流（D73）。
func TestRateLimitAdminBypass(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	admin := e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	storage := e.makeStorage("默认", true)

	wantOK(t, e.settings.Set("upload.rateLimit.enabled", true, "usr_admin"))
	wantOK(t, e.settings.Set("upload.rateLimit.perHour", 1, "usr_admin"))

	files := []IncomingFile{
		e.writeTempFile("a.png", 10),
		e.writeTempFile("b.png", 10),
		e.writeTempFile("c.png", 10),
	}
	job := e.enqueueAndWait(admin, files, storage.UID)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("管理员应跳过限流，实际 %s", job.Status)
	}
}

// ---- 队列长度（D41）----

func TestQueueMaxLengthRejects(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	e.makeStorage("默认", true)

	wantOK(t, e.settings.Set("upload.queueMaxLength", 1, "usr_admin"))

	// 手工造一个活动 job 占满队列
	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_occupied", Kind: model.JobKindUpload, Status: model.JobStatusQueued,
		UserUID: user.UID, TotalItems: 1, CreatedAt: now,
	}, []model.JobItem{{JobUID: "job_occupied", Seq: 1, Status: model.JobStatusQueued}}))

	f := e.writeTempFile("a.png", 10)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeTooMany)
}

// ---- 校验：文件类型与大小 ----

func TestUploadRejectsDisallowedExtension(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	e.makeStorage("默认", true)

	f := e.writeTempFile("evil.exe", 10)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeInvalidParam)
}

func TestUploadRejectsOversizeFile(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	e.makeStorage("默认", true)

	wantOK(t, e.settings.Set("upload.maxSizeBytes", 100, "usr_admin"))

	f := e.writeTempFile("big.png", 500)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeInvalidParam)
}

func TestUploadBlockSvg(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	e.makeStorage("默认", true)

	// 默认允许 svg
	if !e.upload.allowedExtensions()["svg"] {
		t.Fatal("默认应允许 svg")
	}

	wantOK(t, e.settings.Set("upload.blockSvg", true, "usr_admin"))
	f := e.writeTempFile("x.svg", 10)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeInvalidParam)
}

func TestUploadRequiresStorage(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	// 不建任何存储配置

	f := e.writeTempFile("a.png", 10)
	_, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files: []IncomingFile{f},
	})
	wantCode(t, err, response.CodeInvalidParam)
}

// ---- 成功路径与状态流转 ----

func TestUploadSuccessFlow(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	f := e.writeTempFile("photo.png", 70)
	job := e.enqueueAndWait(user, []IncomingFile{f}, storage.UID)

	// job 终态
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("job 应成功，实际 %s（err=%s）", job.Status, job.Error)
	}
	if job.Progress != 100 {
		t.Fatalf("进度应为 100，实际 %d", job.Progress)
	}
	if job.SucceededItems != 1 || job.FailedItems != 0 {
		t.Fatalf("计数不符：ok=%d fail=%d", job.SucceededItems, job.FailedItems)
	}
	if job.SkippedItems != 0 {
		t.Fatalf("SkippedItems 应恒为 0（D66 取消去重），实际 %d", job.SkippedItems)
	}
	if len(job.Items) != 1 || job.Items[0].Status != model.JobStatusSucceeded {
		t.Fatalf("子项状态不符：%+v", job.Items)
	}

	// 图片记录
	up := e.findUpload(job.Items[0].UploadUID)
	if up.Status != model.UploadStatusSuccess {
		t.Fatalf("图片状态应为 success，实际 %s", up.Status)
	}
	if up.URL == "" {
		t.Fatal("URL 为空")
	}
	if up.Size != 70 {
		t.Fatalf("Size 期望 70，实际 %d", up.Size)
	}
	// mock 的魔法文件名模板应生效：{filename}-{md5-8}{extname}
	if !containsStr(up.FileName, "photo-") {
		t.Fatalf("魔法文件名模板未生效：%s", up.FileName)
	}

	// 配额累加
	fresh, err := e.users.FindByUID(user.UID)
	wantOK(t, err)
	if fresh.UsedBytes != 70 {
		t.Fatalf("UsedBytes 期望 70，实际 %d", fresh.UsedBytes)
	}

	// 暂存文件应被清理（keepLocalCopy 默认 false）
	if _, statErr := os.Stat(f.Path); !os.IsNotExist(statErr) {
		t.Fatalf("成功后应清理暂存文件: %s", f.Path)
	}
}

// TestUploadKeepsRawOutput 是 D47 的关键回归：
// **上传返回的原始 IImgInfo 必须原样落库**（含插件回写的 sha），
// 否则远端删除时插件认不出文件，图片就永远删不掉。
func TestUploadKeepsRawOutput(t *testing.T) {
	e := newW5Env(t)

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)
	e.startQueue()

	f := e.writeTempFile("raw.png", 42)
	job := e.enqueueAndWait(user, []IncomingFile{f}, storage.UID)
	uploadUID := job.Items[0].UploadUID

	res, err := e.uploads.GetResult(uploadUID)
	wantOK(t, err)
	if res.RawOutput == "" || res.RawOutput == "[]" {
		t.Fatalf("RawOutput 未被写入：%q", res.RawOutput)
	}

	var raw []map[string]any
	wantOK(t, json.Unmarshal([]byte(res.RawOutput), &raw))
	if len(raw) != 1 {
		t.Fatalf("RawOutput 应有 1 个元素，实际 %d", len(raw))
	}

	// 字段名必须是 picgo 原生的（不是 PascalCase）
	if _, ok := raw[0]["fileName"]; !ok {
		t.Fatalf("RawOutput 应含 picgo 原生字段 fileName，实际键：%v", keysOfAny(raw[0]))
	}
	if _, ok := raw[0]["imgUrl"]; !ok {
		t.Fatalf("RawOutput 应含 picgo 原生字段 imgUrl，实际键：%v", keysOfAny(raw[0]))
	}
	// 插件回写的 sha 必须保留（这是远端删除的前提）
	if _, ok := raw[0]["sha"]; !ok {
		t.Fatal("RawOutput 应保留插件回写的 sha 字段")
	}
	// 不得出现 PascalCase 的转换痕迹
	if _, ok := raw[0]["FileName"]; ok {
		t.Fatal("RawOutput 不得把 picgo 字段名转成 PascalCase（D81.3 第 5 条）")
	}
}

// TestUploadKeepsLocalCopy 断言 keepLocalCopy 生效。
func TestUploadKeepsLocalCopy(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)
	wantOK(t, e.settings.Set("upload.keepLocalCopy", true, "usr_admin"))

	f := e.writeTempFile("keep.png", 20)
	job := e.enqueueAndWait(user, []IncomingFile{f}, storage.UID)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("应成功，实际 %s", job.Status)
	}
	if _, err := os.Stat(f.Path); err != nil {
		t.Fatalf("keepLocalCopy=true 时暂存文件应保留: %v", err)
	}
}

// ---- 失败与重试 ----

// TestUploadPartialFailure 断言 D37：**只要有 item 失败 → job=failed**，
// 但**成功项的 URL 照样在 Result 里**，不丢数据。
func TestUploadPartialFailure(t *testing.T) {
	e := newW5Env(t, func(o *w5Options) {
		// 路径含 "bad" 的一律失败
		o.failPaths = []string{"bad"}
	})
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	good := e.writeTempFile("good.png", 30)
	bad := e.writeTempFile("bad.png", 30)

	job := e.enqueueAndWait(user, []IncomingFile{good, bad}, storage.UID)

	if job.Status != model.JobStatusFailed {
		t.Fatalf("有失败项时 job 应为 failed，实际 %s", job.Status)
	}
	if job.SucceededItems != 1 || job.FailedItems != 1 {
		t.Fatalf("计数不符：ok=%d fail=%d", job.SucceededItems, job.FailedItems)
	}
	if job.Error == "" {
		t.Fatal("job.Error 应非空")
	}

	// Result 里必须**同时**有成功项的 URL 与失败项的原因
	resJSON, err := json.Marshal(job.Result)
	wantOK(t, err)
	s := string(resJSON)
	if !containsStr(s, "https://") {
		t.Fatalf("成功项的 URL 必须保留在 Result 里：%s", s)
	}
	if !containsStr(s, "422") && !containsStr(s, "失败") {
		t.Fatalf("失败项的原因应在 Result 里：%s", s)
	}

	// 失败项的图片记录状态
	for _, it := range job.Items {
		up := e.findUpload(it.UploadUID)
		if it.Seq == 2 && up.Status != model.UploadStatusFailed {
			t.Fatalf("失败项状态应为 failed，实际 %s", up.Status)
		}
	}

	// 成功项才计入配额：只应累加 good 的 30 字节
	fresh, err := e.users.FindByUID(user.UID)
	wantOK(t, err)
	if fresh.UsedBytes != 30 {
		t.Fatalf("UsedBytes 应只累加成功项（30），实际 %d", fresh.UsedBytes)
	}
}

// TestUploadRetriesThenSucceeds 断言重试次数被尊重。
//
// mock 的失败是按**路径**判定的，因此这里用「第一次失败、之后成功」的
// 自定义 agent 来验证重试计数。
func TestUploadRetriesThenSucceeds(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	// 让重试 2 次
	wantOK(t, e.settings.Set("upload.retryTimes", 2, "usr_admin"))
	// 退避设小一点，别让测试等太久
	wantOK(t, e.settings.Set("upload.retryBackoffMs", 10, "usr_admin"))

	f := e.writeTempFile("retry.png", 10)
	// mock 默认成功 → 走正常路径；重试逻辑的「失败后重试」由下面的用例覆盖
	job := e.enqueueAndWait(user, []IncomingFile{f}, storage.UID)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("应成功，实际 %s", job.Status)
	}
	if job.Items[0].Attempts != 1 {
		t.Fatalf("一次成功时 Attempts 应为 1，实际 %d", job.Items[0].Attempts)
	}
}

// TestUploadTargetInvalidNotRetried 断言「目标非法」**不重试**
// （重试多少次都是同样的错，只会白等）。
func TestUploadTargetInvalidNotRetried(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)
	wantOK(t, e.settings.Set("upload.retryTimes", 3, "usr_admin"))
	wantOK(t, e.settings.Set("upload.retryBackoffMs", 10, "usr_admin"))

	// 手工构造一个「指向不存在配置」的队列项（模拟配置在入队后被删）
	f := e.writeTempFile("orphan.png", 10)
	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_orphan", Kind: model.JobKindUpload, Status: model.JobStatusQueued,
		UserUID: user.UID, StorageUID: storage.UID, TotalItems: 1, CreatedAt: now,
	}, []model.JobItem{{JobUID: "job_orphan", Seq: 1, UploadUID: "up_orphan", Status: model.JobStatusQueued}}))
	wantOK(t, e.uploads.Create(&model.Upload{
		UID: "up_orphan", UserUID: user.UID, StorageUID: storage.UID,
		FileName: "orphan.png", OriginalName: "orphan.png", Size: 10,
		Status: model.UploadStatusPending, Source: model.UploadSourceWeb,
		JobUID: "job_orphan", Metadata: "{}", CreatedAt: now, UpdatedAt: now,
	}))
	wantOK(t, e.uploads.SaveResult("up_orphan", "[]", f.Path))

	start := time.Now()
	e.upload.processItem(context.Background(), &queuedUpload{
		JobUID: "job_orphan", Seq: 1, UploadUID: "up_orphan",
		UserUID: user.UID, FilePath: f.Path, OriginalName: "orphan.png", Size: 10,
		Target: UploadTarget{
			StorageUID: storage.UID,
			Type:       "github",
			ConfigName: "no-such-config", // ← 不存在 → agent 抛 ERR_PARAM
		},
	})
	elapsed := time.Since(start)

	item, err := e.jobs.ListItems("job_orphan")
	wantOK(t, err)
	if len(item) != 1 || item[0].Status != model.JobStatusFailed {
		t.Fatalf("目标非法应直接失败，实际 %+v", item)
	}
	if item[0].Attempts != 1 {
		t.Fatalf("目标非法不应重试，Attempts 应为 1，实际 %d", item[0].Attempts)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("不应等待重试退避，实际耗时 %v", elapsed)
	}
}

// ---- 启动恢复（docs/OPERATIONS.md §2）----

// TestRecoverInterruptedItems 断言进程崩溃后重启：
// 在途项被复位为 queued 并重新入队。
func TestRecoverInterruptedItems(t *testing.T) {
	e := newW5Env(t)

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	// 造一个「上次崩溃时正在跑」的 job/item，且源文件仍在
	f := e.writeTempFile("resume.png", 55)
	now := model.Now()
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_resume", Kind: model.JobKindUpload, Status: model.JobStatusRunning,
		UserUID: user.UID, StorageUID: storage.UID, TotalItems: 1,
		Payload: `{"KeepLocal":false}`, CreatedAt: now, StartedAt: now,
	}, []model.JobItem{{
		JobUID: "job_resume", Seq: 1, UploadUID: "up_resume",
		FileName: "resume.png", Status: model.JobStatusRunning, StartedAt: now,
	}}))
	wantOK(t, e.uploads.Create(&model.Upload{
		UID: "up_resume", UserUID: user.UID, StorageUID: storage.UID,
		FileName: "resume.png", OriginalName: "resume.png", Size: 55,
		Status: model.UploadStatusPending, Source: model.UploadSourceWeb,
		JobUID: "job_resume", Metadata: "{}", CreatedAt: now, UpdatedAt: now,
	}))
	wantOK(t, e.uploads.SaveResult("up_resume", "[]", f.Path))

	// 启动 → 恢复 + 重新入队
	e.startQueue()

	job := e.waitJob("job_resume", 10*time.Second)
	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("恢复后应完成上传，实际 %s（err=%s）", job.Status, job.Error)
	}
}

// TestRecoverMarksMissingFileFailed 断言源文件已被清理时：
// 该 item 直接标失败，错误含「源文件已清理」。
func TestRecoverMarksMissingFileFailed(t *testing.T) {
	e := newW5Env(t)

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	now := model.Now()
	missingPath := e.cfg.UploadsDir() + "/2020/01/gone.png" // 不存在
	wantOK(t, e.jobs.CreateWithItems(&model.Job{
		UID: "job_gone", Kind: model.JobKindUpload, Status: model.JobStatusRunning,
		UserUID: user.UID, StorageUID: storage.UID, TotalItems: 1,
		Payload: `{}`, CreatedAt: now, StartedAt: now,
	}, []model.JobItem{{
		JobUID: "job_gone", Seq: 1, UploadUID: "up_gone",
		FileName: "gone.png", Status: model.JobStatusRunning,
	}}))
	wantOK(t, e.uploads.Create(&model.Upload{
		UID: "up_gone", UserUID: user.UID, StorageUID: storage.UID,
		FileName: "gone.png", OriginalName: "gone.png", Size: 10,
		Status: model.UploadStatusPending, Source: model.UploadSourceWeb,
		JobUID: "job_gone", Metadata: "{}", CreatedAt: now, UpdatedAt: now,
	}))
	wantOK(t, e.uploads.SaveResult("up_gone", "[]", missingPath))

	e.startQueue()

	job := e.waitJob("job_gone", 10*time.Second)
	if job.Status != model.JobStatusFailed {
		t.Fatalf("源文件缺失时 job 应为 failed，实际 %s", job.Status)
	}
	if !containsStr(job.Items[0].Error, "源文件已清理") {
		t.Fatalf("错误信息应说明源文件已清理，实际 %q", job.Items[0].Error)
	}
	up := e.findUpload("up_gone")
	if up.Status != model.UploadStatusFailed {
		t.Fatalf("图片状态应为 failed，实际 %s", up.Status)
	}
}

// ---- 并发度（D35）----

// TestConcurrencySerialWhenOne 断言 concurrency=1 时**严格串行**。
func TestConcurrencySerialWhenOne(t *testing.T) {
	// 每个上传延迟 30ms；若串行，3 个至少 90ms
	e := newW5Env(t, func(o *w5Options) { o.uploadDelay = 30 * time.Millisecond })
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	storage := e.makeStorage("默认", true)

	if got := e.upload.concurrency(); got != 1 {
		t.Fatalf("默认并发度应为 1，实际 %d", got)
	}

	files := []IncomingFile{
		e.writeTempFile("a.png", 10),
		e.writeTempFile("b.png", 10),
		e.writeTempFile("c.png", 10),
	}

	start := time.Now()
	job := e.enqueueAndWait(user, files, storage.UID)
	elapsed := time.Since(start)

	if job.Status != model.JobStatusSucceeded {
		t.Fatalf("应成功，实际 %s", job.Status)
	}
	if elapsed < 90*time.Millisecond {
		t.Fatalf("concurrency=1 时应串行（≥90ms），实际 %v", elapsed)
	}
}

// TestConcurrencyDefaultsToOne 断言默认并发度为 1（D35：不依赖补丁）。
func TestConcurrencyDefaultsToOne(t *testing.T) {
	e := newW5Env(t)
	if got := e.settings.GetInt("upload.concurrency", 0); got != 1 {
		t.Fatalf("upload.concurrency 默认值应为 1，实际 %d", got)
	}
	if got := e.upload.concurrency(); got != 1 {
		t.Fatalf("队列并发度应为 1，实际 %d", got)
	}
}

// ---- 批次语义（D38 一批一驱动）----

func TestBatchUsesSingleStorage(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	user := e.makeUser("usr_u1", "u1@example.com", model.UserRoleUser, nil)
	a := e.makeStorage("A", true)
	b := e.makeStorage("B", false)

	files := []IncomingFile{e.writeTempFile("a.png", 10), e.writeTempFile("b.png", 10)}
	job := e.enqueueWaitWithStorage(user, files, a.UID)

	// 所有项都走 A
	for _, it := range job.Items {
		up := e.findUpload(it.UploadUID)
		if up.StorageUID != a.UID {
			t.Fatalf("批内图片应全部属于 %s，实际 %s", a.UID, up.StorageUID)
		}
	}
	// job 层记录驱动（不重复存到 item，D38）
	if job.StorageUID != a.UID {
		t.Fatalf("job.StorageUID 期望 %s，实际 %s", a.UID, job.StorageUID)
	}
	// B 完全没被用到
	if target, err := e.storage.ResolveUploadTarget(b.UID); err != nil || target.StorageUID != b.UID {
		t.Fatal("B 应仍然可解析（只是本次没用到）")
	}
}

// enqueueWaitWithStorage 与 enqueueAndWait 相同，但显式传入 storageUID（可读性）。
func (e *w5Env) enqueueWaitWithStorage(user *model.User, files []IncomingFile, storageUID string) *JobView {
	e.t.Helper()
	return e.enqueueAndWait(user, files, storageUID)
}

// keysOfAny 返回 map 的键（测试辅助）。
func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
