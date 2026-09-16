package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// StorageService 管理存储驱动配置。
//
// 核心约定（D22 + D64 + D78）：
//
//   - **DB 是真相源**：`StorageConfigs`（元数据）+ `StorageSecrets`（凭据，AES-256-GCM）
//   - **config.json 是投影**：变更后同步给 agent；同步时**只覆盖我们管辖的键**
//     （`picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`），
//     插件私有键（如 github-plus 的 `uploaded` 图片账本）**必须原样保留**
//   - **对外一律用 UID**，且**响应中密钥字段一律掩码**
//   - `PicgoConfigName` **创建后只读**（D64 推论：picgo 的 createOrUpdate 是
//     「未命中即新建」语义，允许改名会把同一条配置裂成两条）
type StorageService struct {
	cfg      *config.Config
	log      *slog.Logger
	settings *settings.Service
	cipher   *crypto.Cipher
	repo     *repository.StorageConfigRepo
	agent    agent.Client
	audit    *AuditService

	// schemaCache 缓存「驱动类型 → 敏感字段名集合」。
	//
	// 需要它是因为：脱敏时必须知道哪些字段是密码类（`type == "password"`），
	// 而这份信息来自 agent 的驱动 schema。每次请求都问一遍 agent 太浪费，
	// 因此带 TTL 缓存（D77.2：驱动能力靠探测，不硬编码驱动名）。
	schemaMu    sync.RWMutex
	schemaCache map[string]map[string]bool // type -> fieldName -> isSensitive
	schemaAt    time.Time

	// notice 是可选的系统通知回调（由 RegisterBusiness 注入 Hub 的广播）。
	// 用回调而不是直接依赖 events 包：storage 不该知道「谁在监听」。
	notice func(level, message string)
}

// schemaCacheTTL 驱动 schema 缓存的存活时间。
//
// 取 60s：装插件后最迟 1 分钟生效，而 1 分钟内不会对 agent 造成可感知压力。
const schemaCacheTTL = 60 * time.Second

// NewStorageService 构造。
func NewStorageService(
	cfg *config.Config,
	log *slog.Logger,
	settingsSvc *settings.Service,
	cipher *crypto.Cipher,
	repo *repository.StorageConfigRepo,
	ag agent.Client,
	audit *AuditService,
) *StorageService {
	return &StorageService{
		cfg:      cfg,
		log:      log,
		settings: settingsSvc,
		cipher:   cipher,
		repo:     repo,
		agent:    ag,
		audit:    audit,
	}
}

// StorageConfigView 是对外暴露的存储配置（**密钥一律掩码**）。
type StorageConfigView struct {
	UID             string             `json:"UID"`
	Name            string             `json:"Name"`
	Type            string             `json:"Type"`
	PicgoConfigName string             `json:"PicgoConfigName"`
	Enabled         bool               `json:"Enabled"`
	IsDefault       bool               `json:"IsDefault"`
	PathTemplate    string             `json:"PathTemplate"`
	FileTemplate    string             `json:"FileTemplate"`
	Capabilities    agent.Capabilities `json:"Capabilities"`

	// Config 是脱敏后的驱动配置（敏感字段为 crypto.Mask）。
	//
	// 前端据此回填编辑表单；提交时**掩码字段原样回传 = 不修改**（见 Update/UpdateSecrets）。
	Config map[string]any `json:"Config"`

	// HasSecrets 是否已配置凭据（只给布尔，不给值）。
	HasSecrets bool `json:"HasSecrets"`
	// SecretFields 已填写的密钥字段名列表（**只给字段名**）。
	SecretFields []string `json:"SecretFields"`

	Metadata map[string]any `json:"Metadata"`
	// UploadCount 引用该配置的图片数（删除前提示用）。
	UploadCount int64 `json:"UploadCount"`

	CreatedAt int64 `json:"CreatedAt"`
	UpdatedAt int64 `json:"UpdatedAt"`
}

// StorageListInput 是列表查询入参。
type StorageListInput struct {
	Keyword  string
	Type     string
	Enabled  *bool
	Page     int
	PageSize int
}

// DriverView 是一个可用驱动（含已求值的配置 schema 与能力）。
type DriverView struct {
	Type         string                    `json:"Type"`
	Name         string                    `json:"Name"`
	Builtin      bool                      `json:"Builtin"`
	GuiOnly      bool                      `json:"GuiOnly"`
	Config       []agent.DriverConfigField `json:"Config"`
	Capabilities agent.Capabilities        `json:"Capabilities"`
	ConfigCount  int                       `json:"ConfigCount"`
}

// CreateStorageInput 是新建存储配置的入参。
type CreateStorageInput struct {
	Name            string
	Type            string
	PicgoConfigName string
	Enabled         *bool
	IsDefault       bool
	PathTemplate    string
	FileTemplate    string
	Config          map[string]any
}

// UpdateStorageInput 是更新存储配置的入参（**不含密钥**）。
type UpdateStorageInput struct {
	Name         *string
	Enabled      *bool
	IsDefault    *bool
	PathTemplate *string
	FileTemplate *string
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 返回存储配置列表（密钥掩码）。
func (s *StorageService) List(ctx context.Context, in StorageListInput) ([]StorageConfigView, int64, error) {
	page, size := normalizePage(in.Page, in.PageSize)

	rows, total, err := s.repo.List(repository.StorageListFilter{
		Keyword:  in.Keyword,
		Type:     in.Type,
		Enabled:  in.Enabled,
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询存储配置失败", err)
	}

	counts, err := s.repo.CountUploadsByStorage()
	if err != nil {
		// 统计失败不该让列表整体失败：退化为逐条不显示计数
		s.log.Warn("统计存储配置的图片数失败", "err", err)
		counts = map[string]int64{}
	}

	out := make([]StorageConfigView, 0, len(rows))
	for i := range rows {
		out = append(out, s.toView(ctx, &rows[i], counts[rows[i].UID]))
	}
	return out, total, nil
}

// Get 返回单个存储配置。
func (s *StorageService) Get(ctx context.Context, uid string) (*StorageConfigView, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}
	count, err := s.repo.CountUploads(uid)
	if err != nil {
		s.log.Warn("统计存储配置的图片数失败", "uid", uid, "err", err)
	}
	v := s.toView(ctx, row, count)
	return &v, nil
}

// ListDrivers 返回可用驱动列表（含已求值 schema 与能力）。
func (s *StorageService) ListDrivers(ctx context.Context) ([]DriverView, error) {
	data, err := s.agent.ListUploaders(ctx)
	if err != nil {
		return nil, fromAgent(err)
	}

	// 统计每类驱动已配置的条数（D64：同类型可多条）
	all, err := s.repo.ListAll()
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询存储配置失败", err)
	}
	typeCounts := map[string]int{}
	for _, c := range all {
		typeCounts[c.Type]++
	}

	out := make([]DriverView, 0, len(data.Uploaders))
	for _, u := range data.Uploaders {
		out = append(out, DriverView{
			Type:         u.Type,
			Name:         u.Name,
			Builtin:      u.Builtin,
			GuiOnly:      u.GuiOnly,
			Config:       u.Config,
			Capabilities: u.Capabilities,
			ConfigCount:  typeCounts[u.Type],
		})
	}
	return out, nil
}

// DriverSchema 按当前表单值**重新求值**驱动 schema（处理 DependsOn 联动）。
//
// 必须回 agent 求值：`default` / `choices` 可能是依赖其它字段当前值的**函数**，
// 而前端不执行插件代码（PICGO-INTEGRATION.md §3）。
func (s *StorageService) DriverSchema(ctx context.Context, typ string, answers map[string]any) (*DriverView, error) {
	data, err := s.agent.UploaderSchema(ctx, typ, answers)
	if err != nil {
		return nil, fromAgent(err)
	}
	// 能力从 uploaders 列表补（schema 端点不返回 Capabilities）
	var caps agent.Capabilities
	if list, err := s.agent.ListUploaders(ctx); err == nil {
		for _, u := range list.Uploaders {
			if u.Type == typ {
				caps = u.Capabilities
				break
			}
		}
	}
	return &DriverView{
		Type:         data.Type,
		Name:         data.Name,
		Config:       data.Config,
		Capabilities: caps,
	}, nil
}

// ---------------------------------------------------------------------------
// 写入
// ---------------------------------------------------------------------------

// Create 新建存储配置（元数据 + 凭据在一个事务里落库）。
func (s *StorageService) Create(ctx context.Context, in CreateStorageInput, by, clientIP, userAgent string) (*StorageConfigView, error) {
	name := strings.TrimSpace(in.Name)
	typ := strings.TrimSpace(in.Type)
	if name == "" {
		return nil, Errorf(response.CodeInvalidParam, "配置名不能为空")
	}
	if typ == "" {
		return nil, Errorf(response.CodeInvalidParam, "驱动类型不能为空")
	}
	if len(name) > 64 {
		return nil, Errorf(response.CodeInvalidParam, "配置名不能超过 64 字符")
	}

	picgoConfigName := strings.TrimSpace(in.PicgoConfigName)
	if picgoConfigName == "" {
		picgoConfigName = "Default"
	}
	if len(picgoConfigName) > 64 {
		return nil, Errorf(response.CodeInvalidParam, "PicgoConfigName 不能超过 64 字符")
	}

	// 驱动必须存在（否则后续同步必然失败，不如现在就拒）
	if err := s.assertDriverExists(ctx, typ); err != nil {
		return nil, err
	}

	// 唯一性：展示名全局唯一；且 (Type, PicgoConfigName) 组合唯一（D64）
	if exists, err := s.repo.NameExists(name, ""); err != nil {
		return nil, Wrap(response.CodeInternal, "校验配置名失败", err)
	} else if exists {
		return nil, Errorf(response.CodeConflict, "配置名「%s」已存在", name)
	}
	if exists, err := s.repo.TypeConfigNameExists(typ, picgoConfigName, ""); err != nil {
		return nil, Wrap(response.CodeInternal, "校验配置唯一性失败", err)
	} else if exists {
		return nil, Errorf(response.CodeConflict,
			"驱动 %s 下已存在名为「%s」的配置（PicgoConfigName 创建后不可重复）", typ, picgoConfigName)
	}

	// 凭据：整个 Config 一起加密（StorageConfigs 表没有 config 列，见 DATA-MODEL）
	encPayload, err := s.encryptConfig(in.Config)
	if err != nil {
		return nil, err
	}

	// 先同步给 agent 拿到 _id（picgo 的 uploaderConfig 自己生成），
	// 再以它为我们的 UID —— 这样两边天然一一对应（D65）。
	res, err := s.agent.CreateOrUpdateUploaderConfig(ctx, agent.CreateUploaderConfigInput{
		Type:       typ,
		ConfigName: picgoConfigName,
		Config:     in.Config,
		Activate:   false,
	})
	if err != nil {
		return nil, fromAgent(err)
	}
	uid := picgoConfigID(res, typ, picgoConfigName)

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	now := model.Now()
	row := &model.StorageConfig{
		UID:             uid,
		Name:            name,
		Type:            typ,
		PicgoConfigName: picgoConfigName,
		Enabled:         enabled,
		IsDefault:       false, // 置默认可在创建后单独 activate，避免并发下的双默认
		PathTemplate:    strings.TrimSpace(in.PathTemplate),
		FileTemplate:    strings.TrimSpace(in.FileTemplate),
		Capabilities:    s.capabilitiesJSON(ctx, typ),
		Metadata:        "{}",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.CreateWithSecret(row, encPayload); err != nil {
		// 回滚 agent 侧的配置，避免留下「agent 有、DB 没有」的孤儿
		if delErr := s.agent.DeleteUploaderConfig(ctx, typ, picgoConfigName); delErr != nil {
			s.log.Warn("回滚 agent 侧配置失败（可能留下孤儿配置）",
				"type", typ, "config_name", picgoConfigName, "err", delErr)
		}
		return nil, Wrap(response.CodeInternal, "保存存储配置失败", err)
	}

	if in.IsDefault {
		if err := s.activate(ctx, uid); err != nil {
			s.log.Warn("设为默认失败（配置已创建）", "uid", uid, "err", err)
		}
	}

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeStorageCreate,
		Status:     model.LogStatusSuccess,
		UserUID:    by,
		TargetType: "storage",
		TargetUID:  uid,
		Detail: map[string]any{
			"Name": name, "Type": typ, "PicgoConfigName": picgoConfigName,
			"Enabled": enabled, "IsDefault": in.IsDefault,
			// ⚠️ 密钥一律脱敏，绝不写进审计日志
			"Config": redactForLog(in.Config),
		},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	return s.Get(ctx, uid)
}

// Update 更新元数据与模板（**不含密钥**）。
func (s *StorageService) Update(ctx context.Context, uid string, in UpdateStorageInput, by, clientIP, userAgent string) (*StorageConfigView, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	fields := map[string]any{}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, Errorf(response.CodeInvalidParam, "配置名不能为空")
		}
		if name != row.Name {
			exists, err := s.repo.NameExists(name, uid)
			if err != nil {
				return nil, Wrap(response.CodeInternal, "校验配置名失败", err)
			}
			if exists {
				return nil, Errorf(response.CodeConflict, "配置名「%s」已存在", name)
			}
		}
		fields["Name"] = name
	}
	if in.Enabled != nil {
		fields["Enabled"] = *in.Enabled
	}
	if in.PathTemplate != nil {
		fields["PathTemplate"] = strings.TrimSpace(*in.PathTemplate)
	}
	if in.FileTemplate != nil {
		fields["FileTemplate"] = strings.TrimSpace(*in.FileTemplate)
	}

	if err := s.repo.UpdateFields(uid, fields); err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	// IsDefault 单独处理（需要同事务清掉其他行）
	if in.IsDefault != nil {
		if *in.IsDefault {
			if err := s.activate(ctx, uid); err != nil {
				return nil, err
			}
		} else if row.IsDefault {
			// 取消默认：允许留空（上传时会明确报错），但优先挑另一条启用的
			if err := s.reassignDefault(ctx, uid); err != nil {
				return nil, err
			}
		}
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeStorageUpdate, Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "storage", TargetUID: uid,
		Detail:   map[string]any{"action": "update", "ChangedFields": keysOf(fields)},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	// 元数据变化（模板/启用状态）也要让 agent 知道当前激活项
	if err := s.syncActive(ctx); err != nil {
		s.log.Warn("同步激活配置到内核失败", "err", err)
	}
	return s.Get(ctx, uid)
}

// UpdateSecrets 单独更新凭据（D78：与元数据更新分离）。
//
// 语义：**merge** —— 只提交需要变更的字段；值等于掩码 `******` 时保留原值；
// 显式传 `""` 表示清空该字段。
func (s *StorageService) UpdateSecrets(ctx context.Context, uid string, patch map[string]any, by, clientIP, userAgent string) (*StorageConfigView, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	current, err := s.decryptConfig(uid)
	if err != nil {
		return nil, err
	}

	changed := make([]string, 0, len(patch))
	merged := make(map[string]any, len(current))
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range patch {
		// 掩码 = 不修改（前端回填的就是掩码）
		if s, ok := v.(string); ok && crypto.IsMasked(s) {
			continue
		}
		merged[k] = v
		changed = append(changed, k)
	}

	if len(changed) == 0 {
		// 无实际变更：直接返回当前视图（不写库、不记日志噪音）
		return s.Get(ctx, uid)
	}

	// 同步给 agent（createOrUpdate 是「未命中即新建、命中即更新」）
	if _, err := s.agent.CreateOrUpdateUploaderConfig(ctx, agent.CreateUploaderConfigInput{
		Type:       row.Type,
		ConfigName: row.PicgoConfigName,
		Config:     merged,
		Activate:   row.IsDefault,
	}); err != nil {
		return nil, fromAgent(err)
	}

	enc, err := s.encryptConfig(merged)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpsertSecret(uid, enc); err != nil {
		return nil, Wrap(response.CodeInternal, "保存凭据失败", err)
	}
	if err := s.repo.UpdateFields(uid, map[string]any{"Capabilities": s.capabilitiesJSON(ctx, row.Type)}); err != nil {
		s.log.Warn("刷新驱动能力失败", "uid", uid, "err", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeStorageUpdate, Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "storage", TargetUID: uid,
		// ⚠️ 只记**变更的字段名**，不含值
		Detail:   map[string]any{"action": "secrets", "ChangedFields": changed},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return s.Get(ctx, uid)
}

// StorageDeleteResult 是删除存储配置的结果。
type StorageDeleteResult struct {
	Deleted           bool   `json:"Deleted"`
	AffectedUploads   int64  `json:"AffectedUploads"`
	DefaultSwitchedTo string `json:"DefaultSwitchedTo"`
}

// Delete 删除存储配置。
//
// 约束：
//   - 有图片引用且 `force = false` → 40901（提示先处理图片）
//   - 同时删 `StorageSecrets` 行并调 agent 删除配置
//   - 若删的是默认项，自动把另一条 `Enabled` 的置为默认（D64）
func (s *StorageService) Delete(ctx context.Context, uid string, force bool, by, clientIP, userAgent string) (*StorageDeleteResult, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	count, err := s.repo.CountUploads(uid)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "统计引用图片数失败", err)
	}
	if count > 0 && !force {
		return nil, Errorf(response.CodeConflict, "有 %d 张图片使用该存储配置，请先处理这些图片或强制删除", count)
	}

	if err := s.repo.Delete(uid); err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	// agent 侧删除：失败不阻断（DB 已是真相源，且下次 reconcile 会一致）
	if err := s.agent.DeleteUploaderConfig(ctx, row.Type, row.PicgoConfigName); err != nil {
		s.log.Warn("删除内核侧配置失败（DB 已删，配置会成为孤儿）",
			"type", row.Type, "config_name", row.PicgoConfigName, "err", err)
	}

	result := &StorageDeleteResult{Deleted: true, AffectedUploads: count}

	if row.IsDefault {
		if err := s.reassignDefault(ctx, uid, true); err != nil {
			s.log.Warn("重新指定默认存储失败", "err", err)
		}
		if next, err := s.repo.FindDefault(); err == nil && next != nil {
			result.DefaultSwitchedTo = next.UID
		}
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeStorageDelete, Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "storage", TargetUID: uid,
		Detail: map[string]any{
			"Name": row.Name, "Type": row.Type,
			"AffectedUploads": count, "Force": force,
		},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return result, nil
}

// Activate 把某配置置为全局默认。
func (s *StorageService) Activate(ctx context.Context, uid, by, clientIP, userAgent string) (*StorageConfigView, error) {
	if err := s.activate(ctx, uid); err != nil {
		return nil, err
	}
	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeStorageUpdate, Status: model.LogStatusSuccess,
		UserUID: by, TargetType: "storage", TargetUID: uid,
		Detail:   map[string]any{"action": "activate"},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return s.Get(ctx, uid)
}

// StorageTestResult 是连通性测试结果。
type StorageTestResult struct {
	Ok        bool   `json:"Ok"`
	Message   string `json:"Message"`
	LatencyMs int64  `json:"LatencyMs"`
}

// Test 连通性测试（由 agent 真跑一次轻量上传）。
//
// ⚠️ 失败时 HTTP 仍为 200（「测试本身执行成功」），因此这里**不返回 error**，
// 而是把 Ok/Message 放在结果里（docs/API.md §3.2）。
func (s *StorageService) Test(ctx context.Context, uid, by, clientIP, userAgent string) (*StorageTestResult, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	start := time.Now()
	data, err := s.agent.TestUploader(ctx, agent.TestUploaderInput{
		Type:       row.Type,
		ConfigName: row.PicgoConfigName,
	})
	latency := time.Since(start).Milliseconds()

	result := &StorageTestResult{LatencyMs: latency}
	if err != nil {
		// agent 不可用是「环境问题」，要如实区分于「图床配置错」
		result.Ok = false
		result.Message = agent.MessageOf(err)
	} else {
		result.Ok = data.Ok
		result.Message = data.Message
		if data.LatencyMs > 0 {
			result.LatencyMs = data.LatencyMs
		}
		if data.Detail != "" && !data.Ok {
			result.Message = data.Message + "：" + data.Detail
		}
		// 测试是**真上传探针图** —— 顺手完成「服务端改名」探测（与上传同一套
		// 对比逻辑）：URL 文件名 ≠ 实际发送的文件名 ⇒ 该图床无视魔法文件名。
		// 这样管理员点一次「测试连通性」就能看到能力徽章/表单降级，
		// 不必等第一次真实上传。
		if data.Ok {
			s.MarkServerRenameDetected(uid, data.FileName, data.Detail)
		}
	}

	status := model.LogStatusSuccess
	if !result.Ok {
		status = model.LogStatusFailed
	}
	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeStorageUpdate, Status: status,
		UserUID: by, TargetType: "storage", TargetUID: uid,
		Detail:   map[string]any{"action": "test", "Ok": result.Ok, "LatencyMs": result.LatencyMs},
		Cause:    err,
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return result, nil
}

// ---------------------------------------------------------------------------
// 上传链路的解析入口
// ---------------------------------------------------------------------------

// UploadTarget 是上传时需要的「目标 + 模板」快照。
type UploadTarget struct {
	StorageUID string
	Type       string
	ConfigName string

	PathTemplate string
	FileTemplate string
	// SupportsPathTemplate 驱动是否支持自定义远端路径；
	// false 时 agent 会把路径降级为文件名前缀（D44）。
	SupportsPathTemplate bool
}

// CapabilitiesOf 返回某存储配置的能力（读取当前缓存；不存在时报错）。
// 供测试与未来的只读场景使用；避免暴露内部 repo。
func (s *StorageService) CapabilitiesOf(uid string) (agent.Capabilities, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return agent.Capabilities{}, err
	}
	return parseCapabilities(row.Capabilities), nil
}

// MarkServerRenameDetected 运行时探测「服务端改名」并回写（PicList 同款）。
//
// picgo 协议不声明驱动是否尊重传入文件名，只能实测：上传成功后对比
// 返回 URL 的文件名与期望名（忽略扩展名与路径结构）。不一致 ⇒ 该图床
// 无视魔法文件名（如 NodeImage 强制短链 ID），置位 Capabilities.ServerRenames
// （D77 运行时探测）。幂等：已置位时不重复写；存储不存在时静默忽略。
// 返回是否发生了置位（仅供测试断言）。
func (s *StorageService) MarkServerRenameDetected(storageUID, expectedFileName, url string) bool {
	if url == "" || expectedFileName == "" || storageUID == "" {
		return false
	}
	if urlFileName(url) == "" {
		return false // URL 无文件名（无法对比，不判定）
	}
	if stripExt(urlFileName(url)) == stripExt(expectedFileName) {
		return false // 一致：尊重传入名
	}

	row, err := s.repo.FindByUID(storageUID)
	if err != nil {
		return false // 不存在 / 查询失败：静默忽略
	}
	caps := parseCapabilities(row.Capabilities)
	if caps.ServerRenames {
		return false // 已置位（幂等）
	}
	caps.ServerRenames = true
	caps.DetectedAt = model.Now()
	raw, err := json.Marshal(caps)
	if err != nil {
		return false
	}
	if err := s.repo.UpdateFields(storageUID, map[string]any{"Capabilities": string(raw)}); err != nil {
		s.log.Debug("回写服务端改名探测结果失败", "storage", storageUID, "err", err)
		return false
	}
	return true
}

// urlFileName 从 URL 中取出最后一个路径段的文件名（已去查询串/锚点）。
func urlFileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return ""
	}
	return path.Base(u.Path)
}

// stripExt 去掉扩展名（保留主体）。
//
// 点开头的隐藏文件（如 `.hidden`）没有扩展名语义，原样返回 ——
// 否则 url 为 `https://x/.hidden` 这类场景会误判。
func stripExt(name string) string {
	if strings.HasPrefix(name, ".") && !strings.Contains(name[1:], ".") {
		return name
	}
	return strings.TrimSuffix(name, path.Ext(name))
}

// ResolveUploadTarget 把 StorageUID 解析成上传所需的目标快照。
//
// `uid` 为空时取全局默认配置；没有默认配置时**明确报错**，
// 而不是悄悄选第一条 —— 上传到错误的图床是用户最难发现的故障。
func (s *StorageService) ResolveUploadTarget(uid string) (*UploadTarget, error) {
	var row *model.StorageConfig
	var err error

	if strings.TrimSpace(uid) == "" {
		row, err = s.repo.FindDefault()
		if err != nil {
			return nil, Wrap(response.CodeInternal, "查询默认存储配置失败", err)
		}
		if row == nil {
			return nil, Errorf(response.CodeInvalidParam, "尚未配置默认存储驱动，请先到「存储驱动」里添加并激活一个")
		}
	} else {
		row, err = s.repo.FindByUID(uid)
		if err != nil {
			return nil, notFoundOr(err, "存储配置不存在")
		}
	}

	if !row.Enabled {
		return nil, Errorf(response.CodeInvalidParam, "存储配置「%s」已被禁用", row.Name)
	}

	target := &UploadTarget{
		StorageUID:   row.UID,
		Type:         row.Type,
		ConfigName:   row.PicgoConfigName,
		PathTemplate: row.PathTemplate,
		FileTemplate: row.FileTemplate,
	}
	target.SupportsPathTemplate = s.supportsPathTemplate(row)

	// 未探测过能力时补一次（例如配置是手工写进库的）
	if target.SupportsPathTemplate == false && strings.TrimSpace(row.Capabilities) == "" {
		if caps := s.probeCapabilities(nil, row.Type); caps != nil {
			target.SupportsPathTemplate = caps.SupportsPathTemplate
		}
	}
	return target, nil
}

// StorageName 返回存储配置的展示名（列表页展示用，避免二次查询）。
func (s *StorageService) StorageName(uid string) string {
	if strings.TrimSpace(uid) == "" {
		return ""
	}
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return ""
	}
	return row.Name
}

// NameMap 返回 uid → name 映射（列表页批量用，避免 N+1）。
func (s *StorageService) NameMap() map[string]string {
	rows, err := s.repo.ListAll()
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.UID] = r.Name
	}
	return out
}

// ---------------------------------------------------------------------------
// reconcile（启动时与变更后）
// ---------------------------------------------------------------------------

// Reconcile 以 DB 为真相源重建 agent 侧的存储配置（**幂等**）。
//
// 流程：
//  1. 所有 `Enabled` 配置按 `UpdatedAt` 升序推给 agent（createOrUpdate）
//  2. 最后把 `IsDefault` 的那条设为当前上传器
//
// 幂等：重复执行结果一致；agent 重启后也应再跑一次。
//
// ⚠️ 只覆盖我们管辖的键（D22）：agent 侧的 PATCH/创建都是「键级」操作，
// 插件私有键不受影响。
func (s *StorageService) Reconcile(ctx context.Context) error {
	rows, err := s.repo.ListEnabled()
	if err != nil {
		return Wrap(response.CodeInternal, "查询启用的存储配置失败", err)
	}
	if len(rows) == 0 {
		s.log.Info("没有启用的存储配置，跳过 reconcile")
		return nil
	}

	var firstErr error
	for i := range rows {
		row := &rows[i]
		cfg, err := s.decryptConfig(row.UID)
		if err != nil {
			s.log.Warn("解密存储配置失败，跳过该条", "uid", row.UID, "err", err)
			s.audit.Log(ctx, AuditEntry{
				Type: model.LogTypeThemeError, Status: model.LogStatusFailed,
				TargetType: "storage", TargetUID: row.UID,
				Cause: err,
			})
			continue
		}
		if _, err := s.agent.CreateOrUpdateUploaderConfig(ctx, agent.CreateUploaderConfigInput{
			Type:       row.Type,
			ConfigName: row.PicgoConfigName,
			Config:     cfg,
			Activate:   false,
		}); err != nil {
			s.log.Warn("同步存储配置到内核失败",
				"uid", row.UID, "type", row.Type, "err", err)
			if firstErr == nil {
				firstErr = fromAgent(err)
			}
		}
	}

	if err := s.syncActive(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	s.log.Info("存储配置 reconcile 完成", "count", len(rows))
	return firstErr
}

// syncActive 让 agent 把当前上传器切到 DB 里的默认配置。
func (s *StorageService) syncActive(ctx context.Context) error {
	def, err := s.repo.FindDefault()
	if err != nil {
		return Wrap(response.CodeInternal, "查询默认配置失败", err)
	}
	if def == nil {
		return nil
	}
	if err := s.agent.UseUploader(ctx, def.Type, def.PicgoConfigName); err != nil {
		return fromAgent(err)
	}
	return nil
}

// activate 置为唯一默认并同步给 agent。
func (s *StorageService) activate(ctx context.Context, uid string) error {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return notFoundOr(err, "存储配置不存在")
	}
	if !row.Enabled {
		return Errorf(response.CodeInvalidParam, "已禁用的配置不能设为默认")
	}
	if err := s.repo.SetDefault(uid); err != nil {
		return notFoundOr(err, "存储配置不存在")
	}
	if err := s.agent.UseUploader(ctx, row.Type, row.PicgoConfigName); err != nil {
		// DB 已改为默认；agent 失败只记警告（下次 reconcile 会修好）
		s.log.Warn("切换内核当前上传器失败", "uid", uid, "err", err)
	}
	return nil
}

// reassignDefault 重新指定默认配置。
//
// exclude 用于「刚被删除的那条」——它已经不在库里了，但传进来更显式、更安全。
func (s *StorageService) reassignDefault(ctx context.Context, exclude string, excludeFirst ...bool) error {
	candidates, err := s.repo.ListEnabled()
	if err != nil {
		return Wrap(response.CodeInternal, "查询启用的存储配置失败", err)
	}

	var next *model.StorageConfig
	for i := range candidates {
		if candidates[i].UID == exclude {
			continue
		}
		next = &candidates[i]
		break
	}

	// 没有候选：清空默认（上传时会明确报错「尚未配置默认存储驱动」）
	if next == nil {
		if err := s.repo.SetDefault(""); err != nil {
			return Wrap(response.CodeInternal, "清空默认配置失败", err)
		}
		s.hubNotice("warn", "已无启用的存储驱动，上传将不可用")
		return nil
	}
	return s.activate(ctx, next.UID)
}

// hubNotice 允许 storage service 发系统通知（由 RegisterBusiness 注入 Hub）。
func (s *StorageService) hubNotice(level, message string) {
	if s.notice == nil {
		return
	}
	s.notice(level, message)
}

// notice 是注入的通知回调。
func (s *StorageService) SetNotice(fn func(level, message string)) { s.notice = fn }

// ---------------------------------------------------------------------------
// 内部：加密、脱敏、能力
// ---------------------------------------------------------------------------

func (s *StorageService) encryptConfig(cfg map[string]any) (string, error) {
	if len(cfg) == 0 {
		// 允许「先建元数据、后填凭据」的流程
		return "", nil
	}
	if s.cipher == nil {
		return "", Errorf(response.CodeInternal, "未配置加密主密钥，无法保存凭据")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", Wrap(response.CodeInternal, "序列化凭据失败", err)
	}
	enc, err := s.cipher.Encrypt(raw)
	if err != nil {
		return "", Wrap(response.CodeInternal, "加密凭据失败", err)
	}
	return enc, nil
}

// decryptConfig 读出并解密凭据；无凭据时返回空 map（不是错误）。
func (s *StorageService) decryptConfig(uid string) (map[string]any, error) {
	enc, err := s.repo.GetSecret(uid)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "读取凭据失败", err)
	}
	if strings.TrimSpace(enc) == "" {
		return map[string]any{}, nil
	}
	if s.cipher == nil {
		return nil, Errorf(response.CodeInternal, "未配置加密主密钥，无法读取凭据")
	}
	plain, err := s.cipher.Decrypt(enc)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "解密凭据失败（主密钥可能已更换）", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, Wrap(response.CodeInternal, "凭据格式损坏", err)
	}
	return out, nil
}

// toView 把模型转成对外视图（**敏感字段掩码**）。
func (s *StorageService) toView(ctx context.Context, row *model.StorageConfig, uploadCount int64) StorageConfigView {
	caps := parseCapabilities(row.Capabilities)

	// 解密后再脱敏：能给前端展示非敏感字段（如 repo / url），便于编辑回填
	cfg, err := s.decryptConfig(row.UID)
	if err != nil {
		// 解密失败不阻断列表（例如主密钥换过）——只是 Config 为空
		s.log.Warn("读取凭据失败，Config 将为空", "uid", row.UID, "err", err)
		cfg = map[string]any{}
	}

	sensitive := s.sensitiveFields(ctx, row.Type)
	redacted := make(map[string]any, len(cfg))
	secretFields := make([]string, 0, len(cfg))
	for k, v := range cfg {
		if sensitive[k] || looksSensitive(k) {
			redacted[k] = crypto.Mask
			secretFields = append(secretFields, k)
			continue
		}
		redacted[k] = v
	}
	sortStrings(secretFields)

	return StorageConfigView{
		UID:             row.UID,
		Name:            row.Name,
		Type:            row.Type,
		PicgoConfigName: row.PicgoConfigName,
		Enabled:         row.Enabled,
		IsDefault:       row.IsDefault,
		PathTemplate:    row.PathTemplate,
		FileTemplate:    row.FileTemplate,
		Capabilities:    caps,
		Config:          redacted,
		HasSecrets:      len(cfg) > 0,
		SecretFields:    secretFields,
		Metadata:        parseJSONMap(row.Metadata),
		UploadCount:     uploadCount,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

// sensitiveFields 返回某驱动类型下应视为敏感的字段名集合。
//
// 数据来自 agent 的驱动 schema（`type == "password"`），带 TTL 缓存。
// **不硬编码驱动名或字段名**（D77.2）。
func (s *StorageService) sensitiveFields(ctx context.Context, typ string) map[string]bool {
	s.schemaMu.RLock()
	if s.schemaCache != nil && time.Since(s.schemaAt) < schemaCacheTTL {
		out := s.schemaCache[typ]
		s.schemaMu.RUnlock()
		if out != nil {
			return out
		}
		return map[string]bool{}
	}
	s.schemaMu.RUnlock()

	list, err := s.agent.ListUploaders(ctx)
	if err != nil {
		s.log.Debug("拉取驱动 schema 失败，脱敏将退化为名字启发式", "err", err)
		return map[string]bool{}
	}

	cache := make(map[string]map[string]bool, len(list.Uploaders))
	for _, u := range list.Uploaders {
		fields := make(map[string]bool, len(u.Config))
		for _, f := range u.Config {
			if strings.EqualFold(f.Type, "password") {
				fields[f.Name] = true
			}
		}
		cache[u.Type] = fields
	}

	s.schemaMu.Lock()
	s.schemaCache = cache
	s.schemaAt = time.Now()
	s.schemaMu.Unlock()

	if out := cache[typ]; out != nil {
		return out
	}
	return map[string]bool{}
}

// looksSensitive 是**退化用**的名字启发式：agent 不可用时仍能挡住最明显的密钥字段。
//
// 与 schema 判定是「或」的关系 —— 宁可多掩码一个字段（用户重填一次），
// 也不要把密码明文吐回前端。
func looksSensitive(name string) bool {
	n := strings.ToLower(name)
	for _, kw := range []string{"password", "secret", "token", "apikey", "api_key", "accesskey", "privatekey", "credential"} {
		if strings.Contains(n, kw) {
			return true
		}
	}
	return false
}

// redactForLog 把配置里的敏感值替换成掩码（写审计日志前调用）。
func redactForLog(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if looksSensitive(k) {
			out[k] = crypto.Mask
			continue
		}
		out[k] = v
	}
	return out
}

// capabilitiesJSON 向 agent 探测一次驱动能力并序列化（供缓存进 StorageConfigs）。
func (s *StorageService) capabilitiesJSON(ctx context.Context, typ string) string {
	caps := s.probeCapabilities(ctx, typ)
	if caps == nil {
		return "{}"
	}
	raw, err := json.Marshal(caps)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// probeCapabilities 取某驱动的能力（agent 不可用时返回 nil）。
func (s *StorageService) probeCapabilities(ctx context.Context, typ string) *agent.Capabilities {
	if ctx == nil {
		ctx = context.Background()
	}
	list, err := s.agent.ListUploaders(ctx)
	if err != nil {
		s.log.Debug("探测驱动能力失败", "type", typ, "err", err)
		return nil
	}
	for _, u := range list.Uploaders {
		if u.Type == typ {
			caps := u.Capabilities
			if caps.DetectedAt == 0 {
				caps.DetectedAt = model.Now()
			}
			return &caps
		}
	}
	return nil
}

// supportsPathTemplate 从缓存的 Capabilities 判断驱动是否支持自定义远端路径。
//
// 缓存为空（老数据）时返回 false —— **保守**：宁可降级成文件名前缀，
// 也不要把路径拼错导致图片落到意料之外的位置。
func (s *StorageService) supportsPathTemplate(row *model.StorageConfig) bool {
	caps := parseCapabilities(row.Capabilities)
	return caps.SupportsPathTemplate
}

// supportsRemoteDelete 从缓存的 Capabilities 判断驱动是否支持远端删除。
func (s *StorageService) supportsRemoteDelete(row *model.StorageConfig) bool {
	caps := parseCapabilities(row.Capabilities)
	return caps.SupportsRemoteDelete
}

// FindByUID 暴露给上传服务（内部使用，返回模型）。
func (s *StorageService) FindByUID(uid string) (*model.StorageConfig, error) {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}
	return row, nil
}

// SupportsRemoteDeleteOf 判断某存储配置的驱动是否支持远端删除（D47）。
func (s *StorageService) SupportsRemoteDeleteOf(uid string) bool {
	row, err := s.repo.FindByUID(uid)
	if err != nil {
		return false
	}
	return s.supportsRemoteDelete(row)
}

// assertDriverExists 校验驱动类型存在（来自 agent 的实时列表）。
func (s *StorageService) assertDriverExists(ctx context.Context, typ string) error {
	list, err := s.agent.ListUploaders(ctx)
	if err != nil {
		// agent 不可用时不阻断创建？—— 不，必须阻断：
		// 否则会创建出「DB 有、agent 无法同步」的配置，用户以为配好了却传不上去。
		return fromAgent(err)
	}
	for _, u := range list.Uploaders {
		if u.Type == typ {
			return nil
		}
	}
	return Errorf(response.CodeInvalidParam, "驱动类型「%s」不存在（可能插件未安装或未启用）", typ)
}

// picgoConfigID 从 agent 返回的配置项里取 `_id` 作为我们的 UID。
//
// 退化值（没有 `_id` 时）用确定性字符串，保证同一 (type, name) 永远得到同一个 UID ——
// 这样重复 reconcile 不会产生多条记录。
func picgoConfigID(res *agent.UploaderConfigResult, typ, configName string) string {
	if res != nil && res.Config != nil {
		if v, ok := res.Config["_id"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return fmt.Sprintf("st_%s_%s", sanitizeID(typ), sanitizeID(configName))
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "default"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	// StorageConfigs.UID 列宽 32，这里再截断以确保安全
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = DefaultPage
	}
	if size <= 0 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	return page, size
}

func notFoundOr(err error, message string) error {
	if repository.IsNotFound(err) {
		return Errorf(response.CodeNotFound, "%s", message)
	}
	return Wrap(response.CodeInternal, "", err)
}

func parseCapabilities(raw string) agent.Capabilities {
	var caps agent.Capabilities
	if strings.TrimSpace(raw) == "" {
		return caps
	}
	_ = json.Unmarshal([]byte(raw), &caps)
	return caps
}

func parseJSONMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	// 小切片，插入排序足够且不引入 sort 依赖的额外分配路径
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
