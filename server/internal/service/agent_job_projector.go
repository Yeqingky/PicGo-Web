package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// pluginJobKindPrefix 是插件类 job Kind 的公共前缀（plugin.install / uninstall / update）。
const pluginJobKindPrefix = "plugin."

// defaultReconcileEvery 是对账扫描周期。
//
// 事件驱动结算（job.finished）在 bridge 断线、事件早于建记录到达等
// 情况下会丢；对账是兕底：主动向 agent 查询非终结插件任务的真实状态。
const defaultReconcileEvery = 15 * time.Second

// AgentJobProjector 把 agent 侧的插件类任务投影到 Go 的持久化表。
//
// 背景（bug 修复）：插件安装/卸载/更新的 job 由 agent 创建并持有
// **执行期状态**（内存，重启即丢）；而前端的任务详情/日志查的是
// Go 的 `Jobs` / `JobLogs` 表（docs/API.md §8）。若插件 job 只存在于
// agent 内存，任务详情抽屉会一直报「加载任务失败」「暂无日志输出」。
//
// 三条落库路径：
//
//  1. 事件：agent 的 `job.log` / `job.finished`（经 bridge 转发）→
//     追加日志行 / 结算 Jobs
//  2. 对账：每 defaultReconcileEvery 扫一遍非终结插件任务，向 agent
//     查询真实状态 —— 任务已终结则补结算；agent 已重启丢任务
//     （404）则置失败。保证任务**永远不会永久卡在「进行中」**
//  3. 启动恢复：Go 启动时把插件类非终结任务置为失败（见
//     `JobRepo.FailOrphanedPlugins`）
//
// **只处理「Jobs 表已存在且 Kind 为插件类」的任务**，双保险：
//   - 上传 job 由 UploadService 全权管理（且它发的 `job.finished` 是
//     结构体而非 map，类型断言天然过滤），本组件绝不碰
//   - 表里不存在的 JobUID（竞态 / agent 重放）直接忽略
type AgentJobProjector struct {
	jobs *repository.JobRepo
	ag   agent.Client
	log  *slog.Logger

	// reconcileEvery 是对账周期；0 用默认值，负数禁用对账（测试用）。
	reconcileEvery time.Duration
}

// NewAgentJobProjector 构造。
func NewAgentJobProjector(jobs *repository.JobRepo, ag agent.Client, log *slog.Logger) *AgentJobProjector {
	return &AgentJobProjector{jobs: jobs, ag: ag, log: log, reconcileEvery: defaultReconcileEvery}
}

// Run 恢复孤儿任务并持续消费 Hub 事件 + 周期对账，直到 ctx 取消或 Hub 关闭。
// 应在独立 goroutine 中调用。
func (p *AgentJobProjector) Run(ctx context.Context, hub *events.Hub) {
	// 先订阅再恢复孤儿任务：恢复期间 agent 仍可能发布 job 事件，
	// 不能让启动阶段的事件落在订阅建立之前而丢失。
	var ch <-chan events.Event
	var cancel context.CancelFunc
	if hub != nil {
		ch, cancel = hub.Subscribe("", true) // 系统订阅者：接收全部事件
		defer cancel()
	}

	p.recoverOrphaned()

	interval := p.reconcileEvery
	if interval == 0 {
		interval = defaultReconcileEvery
	}
	// 负数 = 禁用对账：tickCh 保持 nil（select 上接收 nil channel 永久阻塞）
	var tickCh <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tickCh = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				// Hub 已关闭（进程退出中）
				return
			}
			p.handle(ev)
		case <-tickCh:
			p.reconcile(ctx)
		}
	}
}

// recoverOrphaned 在启动时把插件类非终结任务置为 failed。
func (p *AgentJobProjector) recoverOrphaned() {
	n, err := p.jobs.FailOrphanedPlugins()
	if err != nil {
		if p.log != nil {
			p.log.Warn("恢复孤儿插件任务失败", "err", err)
		}
		return
	}
	if n > 0 && p.log != nil {
		p.log.Info("已将孤儿插件任务标记为失败", "count", n)
	}
}

// handle 处理一条 Hub 事件。
func (p *AgentJobProjector) handle(ev events.Event) {
	data, ok := ev.Data.(map[string]any)
	if !ok {
		// Go 侧自己发的 typed 事件（上传 job）或其他无 payload 事件
		return
	}

	switch ev.Name {
	case events.EventJobLog:
		p.onJobLog(data)
	case events.EventJobFinished:
		p.onJobFinished(data)
	}
}

// onJobLog 把 agent 的 job.log 追加到 JobLogs 表。
func (p *AgentJobProjector) onJobLog(data map[string]any) {
	jobUID := mapString(data, "JobUID")
	line := mapString(data, "Line")
	if jobUID == "" || line == "" {
		return
	}
	job, err := p.jobs.FindByUID(jobUID)
	if err != nil || !isPluginJobKind(job.Kind) {
		return
	}
	if _, err := p.jobs.AppendLog(jobUID, line); err != nil && p.log != nil {
		p.log.Warn("插件任务日志落库失败", "job", jobUID, "err", err)
	}
}

// onJobFinished 结算插件任务。
func (p *AgentJobProjector) onJobFinished(data map[string]any) {
	jobUID := mapString(data, "JobUID")
	if jobUID == "" {
		return
	}
	job, err := p.jobs.FindByUID(jobUID)
	if err != nil || !isPluginJobKind(job.Kind) {
		return
	}
	// 已终结就不再覆盖（agent 只发一次 finished，这里防重放/乱序）
	if job.Finished() {
		return
	}

	status := mapString(data, "Status")
	if status != model.JobStatusSucceeded && status != model.JobStatusFailed {
		return
	}

	fields := map[string]any{
		"Status":     status,
		"Progress":   100,
		"FinishedAt": model.Now(),
		"Error":      mapString(data, "Error"),
	}
	if raw, ok := data["Result"]; ok && raw != nil {
		if encoded, err := json.Marshal(raw); err == nil {
			fields["Result"] = string(encoded)
		}
	}
	if err := p.jobs.UpdateFields(jobUID, fields); err != nil && p.log != nil {
		p.log.Warn("插件任务结算落库失败", "job", jobUID, "err", err)
	}
}

// reconcile 周期对账：向 agent 查询每个非终结插件任务的真实状态。
//
// 兕底三类丢失：bridge 断线期间错过 finished 事件、事件早于建记录到达、
// agent 换进程后事件源消失。agent 不可达时本轮跳过（下轮再试）。
func (p *AgentJobProjector) reconcile(ctx context.Context) {
	active, err := p.jobs.ListActive()
	if err != nil {
		if p.log != nil {
			p.log.Warn("对账扫描失败（查询任务）", "err", err)
		}
		return
	}

	for i := range active {
		job := &active[i]
		if !isPluginJobKind(job.Kind) {
			continue // 上传任务归 UploadService
		}
		if ctx.Err() != nil {
			return
		}

		info, err := p.ag.GetJob(ctx, job.UID)
		if err != nil {
			if agent.CodeOf(err) == response.CodeNotFound {
				// agent 在响应但已不认识这个任务 → 它换过进程（内存任务全丢）。
				// 真实结果不可知，明确置失败而不是永远显示「进行中」。
				if err := p.jobs.UpdateFields(job.UID, map[string]any{
					"Status":     model.JobStatusFailed,
					"Progress":   100,
					"FinishedAt": model.Now(),
					"Error":      "内核重启导致任务结果未知",
				}); err != nil && p.log != nil {
					p.log.Warn("对账置失败落库错误", "job", job.UID, "err", err)
				} else if p.log != nil {
					p.log.Info("对账：agent 已丢失该任务，置为失败", "job", job.UID)
				}
			}
			// agent 不可达（连接错误）或查询失败 → 跳过本轮，下轮再试
			continue
		}

		if info.Status != model.JobStatusSucceeded && info.Status != model.JobStatusFailed {
			continue // agent 侧仍在跑，等事件或下轮
		}
		if job.Finished() {
			continue // 已被事件路径结算
		}
		p.settleFromInfo(job.UID, info)
	}
}

// settleFromInfo 用 agent 的任务快照结算 Go 侧记录。
func (p *AgentJobProjector) settleFromInfo(jobUID string, info *agent.JobInfo) {
	finishedAt := info.FinishedAt
	if finishedAt <= 0 {
		finishedAt = model.Now()
	}
	fields := map[string]any{
		"Status":     info.Status,
		"Progress":   info.Progress,
		"FinishedAt": finishedAt,
		"Error":      info.Error,
	}
	if info.Result != nil {
		if encoded, err := json.Marshal(info.Result); err == nil {
			fields["Result"] = string(encoded)
		}
	}
	if err := p.jobs.UpdateFields(jobUID, fields); err != nil && p.log != nil {
		p.log.Warn("对账结算落库失败", "job", jobUID, "err", err)
	}
}

// isPluginJobKind 判断 job Kind 是否为插件类（plugin.install / uninstall / update）。
func isPluginJobKind(kind string) bool {
	return strings.HasPrefix(kind, pluginJobKindPrefix)
}

// mapString 从 bridge 解析出的事件 payload 里安全取字符串字段。
func mapString(data map[string]any, key string) string {
	if v, ok := data[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
