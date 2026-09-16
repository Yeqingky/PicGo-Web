package service

import (
	"context"
	"testing"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// waitFor 轮询等待条件成立（投影器是异步消费事件，不能同步断言）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待条件超时（%s）", timeout)
}

// makePluginJob 预置一条插件任务记录（模拟 pluginAction 落库后的状态）。
func makePluginJob(t *testing.T, e *w5Env, uid, kind, status string) {
	t.Helper()
	now := model.Now()
	job := &model.Job{
		UID: uid, Kind: kind, Status: status,
		UserUID: "up_admin", TotalItems: 1,
		Payload: `{"Names":["picgo-plugin-x"],"Action":"install"}`,
		CreatedAt: now, StartedAt: now,
	}
	if err := e.jobs.CreateWithItems(job, nil); err != nil {
		t.Fatalf("预置插件任务失败: %v", err)
	}
}

// startProjector 启动投影器（独立 goroutine，随测试结束一起回收）。
//
// startProjector(e, -1) 禁用周期对账（避免 mock agent 的 404 干扰事件驱动断言）；
// 传正值（如 20ms）则启用对账供专门测试。
//
// 启动时会同步执行一次孤儿恢复（把预置的 queued/running 插件任务置 failed），
// 因此调用方若要预置“进行中”的测试任务，应先用 waitRecovered 等恢复完成。
func startProjector(e *w5Env, reconcileEvery time.Duration) context.CancelFunc {
	p := NewAgentJobProjector(e.jobs, e.agent, e.log)
	p.reconcileEvery = reconcileEvery
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx, e.hub)
	return cancel
}

// waitRecovered 用一个哨兵孤儿等孤儿恢复完成（确定性同步，避免 sleep 竞态）。
func waitRecovered(t *testing.T, e *w5Env) {
	t.Helper()
	makePluginJob(t, e, "job_recoverprobe", model.JobKindPluginInstall, model.JobStatusRunning)
	waitFor(t, 3*time.Second, func() bool {
		job, err := e.jobs.FindByUID("job_recoverprobe")
		return err == nil && job.Status == model.JobStatusFailed
	})
}

// 投影器应把 agent 的 job.log / job.finished 落到 Jobs / JobLogs 表。
func TestAgentJobProjector_LogAndFinish(t *testing.T) {
	e := newW5Env(t)

	cancel := startProjector(e, -1)
	defer cancel()
	waitRecovered(t, e) // 等孤儿恢复跑完，再预置“进行中”的测试任务

	const jobUID = "job_plugintest0001"
	makePluginJob(t, e, jobUID, model.JobKindPluginInstall, model.JobStatusRunning)

	// agent 风格的 map 事件（bridge 转发后的形状）
	e.hub.Publish(events.Event{Name: events.EventJobLog, Data: map[string]any{
		"JobUID": jobUID, "Seq": 0, "Line": "[agent] 开始 plugin.install: picgo-plugin-x",
	}})
	e.hub.Publish(events.Event{Name: events.EventJobLog, Data: map[string]any{
		"JobUID": jobUID, "Seq": 1, "Line": "npm warn deprecated foo",
	}})
	e.hub.Publish(events.Event{Name: events.EventJobFinished, Data: map[string]any{
		"JobUID": jobUID, "Status": "succeeded",
		"Result": map[string]any{"Success": true}, "Error": "",
	}})

	waitFor(t, 3*time.Second, func() bool {
		job, err := e.jobs.FindByUID(jobUID)
		return err == nil && job.Finished()
	})

	job, err := e.jobs.FindByUID(jobUID)
	if err != nil {
		t.Fatalf("查询任务失败: %v", err)
	}
	if job.Status != model.JobStatusSucceeded || job.Progress != 100 {
		t.Fatalf("任务未正确结算: status=%s progress=%d", job.Status, job.Progress)
	}
	if job.FinishedAt == 0 {
		t.Fatal("FinishedAt 未写入")
	}
	if job.Result == "" || job.Result[:1] != "{" {
		t.Fatalf("Result 未写入: %q", job.Result)
	}

	logs, err := e.jobs.ListLogs(jobUID, 0, 100)
	if err != nil {
		t.Fatalf("查询日志失败: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("应落库 2 行日志，实际 %d 行", len(logs))
	}
	if logs[0].Line != "[agent] 开始 plugin.install: picgo-plugin-x" {
		t.Fatalf("日志内容不符: %q", logs[0].Line)
	}
}

// 投影器不得碰：表里不存在的任务、非插件类任务（上传 job 由 UploadService 管）、
// 以及 Go 侧自己发的 typed（结构体）事件。
func TestAgentJobProjector_IgnoresUnknownAndNonPlugin(t *testing.T) {
	e := newW5Env(t)

	cancel := startProjector(e, -1)
	defer cancel()
	waitRecovered(t, e)

	const uploadJobUID = "job_uploadtest001"
	makePluginJob(t, e, uploadJobUID, model.JobKindUpload, model.JobStatusRunning)

	// 未知 jobUID（表里不存在）
	e.hub.Publish(events.Event{Name: events.EventJobFinished, Data: map[string]any{
		"JobUID": "job_notindb00001", "Status": "failed", "Error": "boom",
	}})
	// 上传任务的事件（同是 map 形状的 job.log / typed 的 finished）
	e.hub.Publish(events.Event{Name: events.EventJobLog, Data: map[string]any{
		"JobUID": uploadJobUID, "Seq": 0, "Line": "should not be logged",
	}})
	e.hub.Publish(events.Event{Name: events.EventJobFinished, Data: events.JobFinishedPayload{
		JobUID: uploadJobUID, Kind: model.JobKindUpload, Status: "succeeded",
	}})
	time.Sleep(200 * time.Millisecond)

	uploadJob, err := e.jobs.FindByUID(uploadJobUID)
	if err != nil {
		t.Fatalf("查询上传任务失败: %v", err)
	}
	if uploadJob.Status != model.JobStatusRunning {
		t.Fatalf("上传任务被投影器误改: %s", uploadJob.Status)
	}
	logs, _ := e.jobs.ListLogs(uploadJobUID, 0, 100)
	if len(logs) != 0 {
		t.Fatalf("上传任务不应被写日志，实际 %d 行", len(logs))
	}
	if _, err := e.jobs.FindByUID("job_notindb00001"); err == nil {
		t.Fatal("未知任务不应被创建")
	}
}

// 已终结的任务不应被 finished 事件覆盖（防重放/乱序）。
func TestAgentJobProjector_NoDoubleFinish(t *testing.T) {
	e := newW5Env(t)
	const jobUID = "job_plugintest0002"
	makePluginJob(t, e, jobUID, model.JobKindPluginUpdate, model.JobStatusFailed)

	// 手写一个可识别的 FinishedAt：若被覆盖则断言失败
	finishedAt := model.Now() - 3600
	if err := e.jobs.UpdateFields(jobUID, map[string]any{"FinishedAt": finishedAt}); err != nil {
		t.Fatalf("预置 FinishedAt 失败: %v", err)
	}

	cancel := startProjector(e, -1)
	defer cancel()

	e.hub.Publish(events.Event{Name: events.EventJobFinished, Data: map[string]any{
		"JobUID": jobUID, "Status": "succeeded", "Result": map[string]any{"Success": true},
	}})
	time.Sleep(200 * time.Millisecond)

	job, err := e.jobs.FindByUID(jobUID)
	if err != nil {
		t.Fatalf("查询任务失败: %v", err)
	}
	if job.Status != model.JobStatusFailed || job.FinishedAt != finishedAt {
		t.Fatalf("已终结任务被覆盖: status=%s finishedAt=%d", job.Status, job.FinishedAt)
	}
}

// 对账兕底：finished 事件丢失（bridge 断线 / 事件早于建记录）时，
// 周期对账仍能从 agent 拿到真实状态；agent 重启丢任务（404）则置失败。
// 保证任务永远不会永久卡在「进行中」。
func TestAgentJobProjector_ReconcileSettles(t *testing.T) {
	e := newW5Env(t)

	// 先建一个哨兵孤儿，用它的终结确认「启动恢复已跑完」，
	// 之后预置的 running 任务只会被对账碰，不会被启动恢复抢先置败
	makePluginJob(t, e, "job_recoverprobe", model.JobKindPluginInstall, model.JobStatusRunning)
	cancel := startProjector(e, 20*time.Millisecond)
	defer cancel()
	waitFor(t, 3*time.Second, func() bool {
		job, err := e.jobs.FindByUID("job_recoverprobe")
		return err == nil && job.Status == model.JobStatusFailed
	})

	ctx := context.Background()
	// mock agent 里真实存在的任务（已 succeeded）→ 对账应补结算为 succeeded
	uidOK, err := e.agent.InstallPlugins(ctx, []string{"picgo-plugin-a"})
	if err != nil {
		t.Fatalf("mock 建任务失败: %v", err)
	}
	makePluginJob(t, e, uidOK, model.JobKindPluginInstall, model.JobStatusRunning)
	// agent 不认识的任务（模拟 agent 重启丢内存任务）→ 对账应置失败
	const uidLost = "job_lostbyagent1"
	makePluginJob(t, e, uidLost, model.JobKindPluginUpdate, model.JobStatusRunning)

	waitFor(t, 3*time.Second, func() bool {
		j1, err1 := e.jobs.FindByUID(uidOK)
		j2, err2 := e.jobs.FindByUID(uidLost)
		return err1 == nil && j1.Status == model.JobStatusSucceeded &&
			err2 == nil && j2.Status == model.JobStatusFailed
	})

	jLost, _ := e.jobs.FindByUID(uidLost)
	if jLost.Error != "内核重启导致任务结果未知" {
		t.Fatalf("丢失任务应写明原因，实际 %q", jLost.Error)
	}
}

// 启动恢复：插件类非终结任务应被置为 failed（agent 内存任务已随重启丢失）。
func TestAgentJobProjector_RecoversOrphans(t *testing.T) {
	e := newW5Env(t)
	makePluginJob(t, e, "job_orphan00001", model.JobKindPluginInstall, model.JobStatusRunning)
	makePluginJob(t, e, "job_orphan00002", model.JobKindPluginUninstall, model.JobStatusQueued)
	// 上传任务不在恢复范围（由 RecoverInterrupted 重置为 queued 交 worker 重试）
	makePluginJob(t, e, "job_orphanup001", model.JobKindUpload, model.JobStatusRunning)

	startProjector(e, -1)

	waitFor(t, 3*time.Second, func() bool {
		job, err := e.jobs.FindByUID("job_orphan00001")
		return err == nil && job.Status == model.JobStatusFailed
	})

	for uid, want := range map[string]string{
		"job_orphan00001": model.JobStatusFailed,
		"job_orphan00002": model.JobStatusFailed,
		"job_orphanup001": model.JobStatusRunning, // 不动上传任务
	} {
		job, err := e.jobs.FindByUID(uid)
		if err != nil {
			t.Fatalf("查询任务 %s 失败: %v", uid, err)
		}
		if job.Status != want {
			t.Fatalf("任务 %s 状态应为 %s，实际 %s", uid, want, job.Status)
		}
	}
	if job, _ := e.jobs.FindByUID("job_orphan00001"); job.Error == "" {
		t.Fatal("孤儿任务应写明中断原因")
	}
}

// 插件操作应同步在 Jobs 表建记录（详情/日志的查询真相源）。
func TestPluginService_CreatesJobRecord(t *testing.T) {
	e := newW5Env(t)
	plugSvc := NewPluginService(e.log, e.agent, e.hub, e.audit, e.jobs)

	res, err := plugSvc.Install(context.Background(), []string{"picgo-plugin-x"}, "up_admin", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	job, err := e.jobs.FindByUID(res.JobUID)
	if err != nil {
		t.Fatalf("任务记录未创建: %v", err)
	}
	if job.Kind != model.JobKindPluginInstall {
		t.Fatalf("Kind 应为 plugin.install，实际 %s", job.Kind)
	}
	if job.Status != model.JobStatusRunning {
		t.Fatalf("初始状态应为 running，实际 %s", job.Status)
	}
	if job.UserUID != "up_admin" || job.TotalItems != 1 {
		t.Fatalf("归属/子项数不符: user=%s total=%d", job.UserUID, job.TotalItems)
	}
}
