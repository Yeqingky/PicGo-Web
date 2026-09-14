package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// PluginService 管理 picgo 插件（安装 / 卸载 / 更新 / 启停）。
//
// 真相源是 **agent 侧的 node_modules**（不是数据库）：插件是 npm 包，
// 装没装、什么版本，只有读盘才知道。因此本服务**不落库**，
// 只做「转发 + 审计 + 事件广播」。
//
// ⚠️ **插件 = 服务器上的任意代码**：安装/卸载都要求 admin，
// 且每一步都写审计日志（D45 的 plugin.* 类型）。
type PluginService struct {
	log   *slog.Logger
	agent agent.Client
	hub   *events.Hub
	audit *AuditService
}

// NewPluginService 构造。
func NewPluginService(log *slog.Logger, ag agent.Client, hub *events.Hub, audit *AuditService) *PluginService {
	return &PluginService{log: log, agent: ag, hub: hub, audit: audit}
}

// PluginView 是对外的插件对象。
type PluginView struct {
	Name        string `json:"Name"`
	Version     string `json:"Version"`
	Enabled     bool   `json:"Enabled"`
	GuiOnly     bool   `json:"GuiOnly"`
	Uploader    string `json:"Uploader"`
	Transformer string `json:"Transformer"`
	Description string `json:"Description"`
	Author      string `json:"Author"`
	Homepage    string `json:"Homepage"`
}

// PluginListResult 是插件列表结果。
type PluginListResult struct {
	Plugins []PluginView `json:"Plugins"`
	// Disabled 被禁用（在 picgo 配置里标 false）的插件名。
	Disabled []string `json:"Disabled"`
}

// List 返回已安装插件。
func (s *PluginService) List(ctx context.Context) (*PluginListResult, error) {
	data, err := s.agent.ListPlugins(ctx)
	if err != nil {
		return nil, fromAgent(err)
	}

	out := &PluginListResult{Plugins: make([]PluginView, 0, len(data.Plugins)), Disabled: data.Disabled}
	if out.Disabled == nil {
		out.Disabled = []string{}
	}
	for _, p := range data.Plugins {
		out.Plugins = append(out.Plugins, PluginView{
			Name: p.Name, Version: p.Version, Enabled: p.Enabled, GuiOnly: p.GuiOnly,
			Uploader: p.Uploader, Transformer: p.Transformer,
			Description: p.Description, Author: p.Author, Homepage: p.Homepage,
		})
	}
	return out, nil
}

// Readme 返回插件 README 原文（前端转义后渲染，**不要当 HTML 插入**）。
func (s *PluginService) Readme(ctx context.Context, name string) (*agent.PluginReadmeData, error) {
	data, err := s.agent.PluginReadme(ctx, name)
	if err != nil {
		return nil, fromAgent(err)
	}
	return data, nil
}

// 插件操作类型（用于审计与事件文案）。
const (
	pluginActionInstall   = "install"
	pluginActionUninstall = "uninstall"
	pluginActionUpdate    = "update"
)

// PluginJobResult 是插件操作的异步任务引用。
type PluginJobResult struct {
	JobUID string `json:"JobUID"`
	// Notice 提示前端「内核可能重启」（装/卸后需要重启进程才能生效）。
	Notice string `json:"Notice"`
}

// Install 安装插件（异步）。
//
// 安装成功后 agent 会**重启自身进程**以使插件生效；
// 因此这里返回 `Notice` 让前端提示用户「内核正在重启」。
func (s *PluginService) Install(ctx context.Context, names []string, by, clientIP, userAgent string) (*PluginJobResult, error) {
	return s.pluginAction(ctx, pluginActionInstall, names, by, clientIP, userAgent)
}

// Uninstall 卸载插件（异步）。
func (s *PluginService) Uninstall(ctx context.Context, names []string, by, clientIP, userAgent string) (*PluginJobResult, error) {
	return s.pluginAction(ctx, pluginActionUninstall, names, by, clientIP, userAgent)
}

// Update 更新插件（异步）；names 为空表示全部。
func (s *PluginService) Update(ctx context.Context, names []string, by, clientIP, userAgent string) (*PluginJobResult, error) {
	return s.pluginAction(ctx, pluginActionUpdate, names, by, clientIP, userAgent)
}

func (s *PluginService) pluginAction(ctx context.Context, action string, names []string, by, clientIP, userAgent string) (*PluginJobResult, error) {
	// 卸载必须显式给名字（「卸载全部」= 灾难）
	if action == pluginActionUninstall && len(names) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "必须指定要卸载的插件名")
	}
	for i, n := range names {
		names[i] = strings.TrimSpace(n)
		if names[i] == "" {
			return nil, Errorf(response.CodeInvalidParam, "插件名不能为空")
		}
	}

	var jobUID string
	var err error
	switch action {
	case pluginActionInstall:
		jobUID, err = s.agent.InstallPlugins(ctx, names)
	case pluginActionUninstall:
		jobUID, err = s.agent.UninstallPlugins(ctx, names)
	default:
		jobUID, err = s.agent.UpdatePlugins(ctx, names)
	}
	if err != nil {
		s.audit.Log(ctx, AuditEntry{
			Type: pluginLogType(action), Status: model.LogStatusFailed,
			UserUID: by, TargetType: "plugin",
			Detail:   map[string]any{"Names": names, "Action": action},
			Cause:    err,
			ClientIP: clientIP, UserAgent: userAgent,
		})
		return nil, fromAgent(err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type: pluginLogType(action), Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "plugin", TargetUID: jobUID,
		Detail:   map[string]any{"Names": names, "Action": action, "JobUID": jobUID},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	notice := ""
	if action == pluginActionInstall || action == pluginActionUninstall {
		notice = "插件变更后内核会重启以使其生效，期间上传可能短暂不可用"
		if s.hub != nil {
			s.hub.PublishNotice("warn", "内核正在重启以应用插件变更，请稍候")
		}
	}

	return &PluginJobResult{JobUID: jobUID, Notice: notice}, nil
}

// SetEnabled 启用/禁用插件。
//
// 禁用是写 picgo 配置（`picgoPlugins[name] = false`），不涉及 npm；
// 因此**不重启内核**（下次加载时不注册该插件）。
func (s *PluginService) SetEnabled(ctx context.Context, name string, enabled bool, by, clientIP, userAgent string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return Errorf(response.CodeInvalidParam, "插件名不能为空")
	}

	if err := s.agent.SetPluginEnabled(ctx, name, enabled); err != nil {
		s.audit.Log(ctx, AuditEntry{
			Type: model.LogTypePluginUpdate, Status: model.LogStatusFailed,
			UserUID: by, TargetType: "plugin", TargetUID: name,
			Detail:   map[string]any{"Enabled": enabled},
			Cause:    err,
			ClientIP: clientIP, UserAgent: userAgent,
		})
		return fromAgent(err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypePluginUpdate, Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "plugin", TargetUID: name,
		Detail:   map[string]any{"Enabled": enabled},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return nil
}

// pluginLogType 把操作映射到操作日志类型。
func pluginLogType(action string) string {
	switch action {
	case pluginActionInstall:
		return model.LogTypePluginInstall
	case pluginActionUninstall:
		return model.LogTypePluginUninstall
	default:
		return model.LogTypePluginUpdate
	}
}
