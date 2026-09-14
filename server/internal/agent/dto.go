package agent

// 本文件定义与 agent 交换的 DTO。
//
// 命名规则（D81 + D81.3 第 5 条）：
//   - **我们自己的外层字段** → PascalCase（`Type` / `Config` / `Capabilities` …）
//   - **来自 picgo 的原生结构** → 原样保留、不得转换
//     （`RawConfig` / `RawImgInfo` / `DriverConfigField.Name`）

// RawConfig 是 picgo 的原始配置对象（`config.json` 的内容）。
//
// ⚠️ 键名是 picgo 原生的（`picBed` / `uploader` / `picgoPlugins` / `settings`，
// 以及插件私有键如 `uploaded`），**必须原样透传、不得转换**（D81.3 第 5 条）。
//
// 用 `map[string]any` 而不是结构体：picgo 的配置是开放结构，
// 插件可以往里写任意键，结构体会把未声明的键丢掉（那正是 D22 禁止的）。
type RawConfig map[string]any

// RawImgInfo 是 picgo 的 `IImgInfo`（一张图片上传时的完整信息）。
//
// ⚠️ 字段名**原样**：`fileName` / `imgUrl` / `extname` / `width` / `height` /
// `size` / `contentType` / `sha` …
// 删除远端文件时要把它**原封不动**交回插件（`remove` 事件，D47），
// 字段名被改过插件就认不出来了。
//
// 用 map 而不是结构体：插件可以回写任意附加字段（如 github-plus 写 `sha`），
// 结构体会把未知字段丢掉 —— 那会导致这些图片**再也删不掉**。
type RawImgInfo map[string]any

// Envelope 是 agent 的统一响应体。
type Envelope struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
	Data    any    `json:"Data"`
}

// ---------------------------------------------------------------------------
// 健康与生命周期
// ---------------------------------------------------------------------------

// HealthzData 是 `GET /healthz` 的响应。
type HealthzData struct {
	Ok            bool   `json:"Ok"`
	PicgoVersion  string `json:"PicgoVersion"`
	ConfigPath    string `json:"ConfigPath"`
	Uptime        int64  `json:"Uptime"`
	PID           int    `json:"PID"`
	PluginsLoaded int    `json:"PluginsLoaded"`
}

// ---------------------------------------------------------------------------
// 配置
// ---------------------------------------------------------------------------

// PatchConfigResult 是 `PATCH /api/config` 的响应。
type PatchConfigResult struct {
	// Applied 实际写入的点路径（如 picBed.uploader）。
	Applied []string `json:"Applied"`
	// PreservedPluginKeys 被保留的插件私有键（D22：绝不删除）。
	PreservedPluginKeys []string `json:"PreservedPluginKeys"`
}

// ---------------------------------------------------------------------------
// 图床与配置表单
// ---------------------------------------------------------------------------

// DriverConfigField 是驱动的一条配置字段（**已由 agent 用
// evaluatePluginConfig 求值**，前端永不执行插件代码）。
type DriverConfigField struct {
	// Name 是**驱动自己的字段名**（repo / token / path / bucket / secretId …），
	// 由插件定义，**原样、不转换**（D81.3 第 5 条）。
	Name string `json:"Name"`
	// Type 决定前端控件：input / password / list / checkbox / confirm / editor / 其它。
	Type     string `json:"Type"`
	Required bool   `json:"Required"`

	Alias   string `json:"Alias,omitempty"`
	Message string `json:"Message,omitempty"`
	Prefix  string `json:"Prefix,omitempty"`

	// Default 已被求值成静态值（原始可能是函数）。
	Default any `json:"Default,omitempty"`
	// Choices 已被求值成静态数组（原始可能是函数）。
	Choices []any `json:"Choices,omitempty"`
	// DependsOn 是需要联动重求值的依赖字段名。
	DependsOn []string `json:"DependsOn,omitempty"`
}

// Capabilities 是驱动能力探测结果（由 agent 计算，Go 侧缓存进
// StorageConfigs.Capabilities；**不硬编码驱动名列表**，D77.2）。
type Capabilities struct {
	// SupportsPathTemplate 该驱动是否支持自定义远端路径（D44）。
	SupportsPathTemplate bool `json:"SupportsPathTemplate"`
	// SupportsRemoteDelete 该驱动是否支持远端删除（D47：靠插件实现 remove 事件）。
	SupportsRemoteDelete bool `json:"SupportsRemoteDelete"`
	// ConfigFields 该驱动声明的配置字段名（**驱动原生名**）。
	ConfigFields []string `json:"ConfigFields"`
	// PathFieldNames 命中的路径类字段名（推断 SupportsPathTemplate 的依据）。
	PathFieldNames []string `json:"PathFieldNames"`
	DetectedAt     int64    `json:"DetectedAt"`
	PicgoVersion   string   `json:"PicgoVersion"`
}

// UploaderInfo 是一个可用的上传器（图床驱动）。
type UploaderInfo struct {
	Type         string              `json:"Type"`
	Name         string              `json:"Name"`
	Builtin      bool                `json:"Builtin"`
	GuiOnly      bool                `json:"GuiOnly"`
	Config       []DriverConfigField `json:"Config"`
	Capabilities Capabilities        `json:"Capabilities"`
	// ConfigNames 该类型下已存在的多配置名（D64）。
	ConfigNames []string `json:"ConfigNames"`
}

// UploadersData 是 `GET /api/uploaders` 的响应。
type UploadersData struct {
	Uploaders   []UploaderInfo  `json:"Uploaders"`
	Current     CurrentUploader `json:"Current"`
	Transformer string          `json:"Transformer"`
}

// CurrentUploader 是 agent 侧当前激活的上传器。
type CurrentUploader struct {
	Type       string `json:"Type"`
	ConfigName string `json:"ConfigName"`
}

// UploaderSchemaData 是 `POST /api/uploaders/schema` 的响应（联动重求值）。
type UploaderSchemaData struct {
	Type   string              `json:"Type"`
	Name   string              `json:"Name"`
	Config []DriverConfigField `json:"Config"`
}

// UploaderConfigsData 是 `GET /api/uploaders/configs` 的响应。
//
// ⚠️ `Configs` 的元素是 **picgo 的原生配置项**（`_id` / `_configName` 以及驱动字段），
// **键名原样保留、不得转换**（D81.3 第 5 条）。仅外层键是我们自己的 PascalCase。
type UploaderConfigsData struct {
	Type              string      `json:"Type"`
	DefaultConfigName string      `json:"DefaultConfigName"`
	Configs           []RawConfig `json:"Configs"`
}

// UploaderConfigResult 是 `POST /api/uploaders/configs` 的响应。
type UploaderConfigResult struct {
	Type   string    `json:"Type"`
	Config RawConfig `json:"Config"`
}

// TestUploaderData 是 `POST /api/uploaders/test` 的响应。
type TestUploaderData struct {
	Ok        bool   `json:"Ok"`
	Message   string `json:"Message"`
	LatencyMs int64  `json:"LatencyMs"`
	Detail    string `json:"Detail,omitempty"`
}

// TransformersData 是 `GET /api/transformers` 的响应。
type TransformersData struct {
	Current      string            `json:"Current"`
	Transformers []TransformerInfo `json:"Transformers"`
}

// TransformerInfo 是一个 transformer。
type TransformerInfo struct {
	Type string `json:"Type"`
	Name string `json:"Name"`
}

// ---------------------------------------------------------------------------
// 插件
// ---------------------------------------------------------------------------

// PluginInfo 是一个已安装的 picgo 插件。
type PluginInfo struct {
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

// PluginsData 是 `GET /api/plugins` 的响应。
type PluginsData struct {
	Plugins []PluginInfo `json:"Plugins"`
	// Disabled 被禁用（在 config 里标 false）的插件名。
	Disabled []string `json:"Disabled"`
}

// PluginReadmeData 是 `GET /api/plugins/{name}/readme` 的响应。
type PluginReadmeData struct {
	Content string `json:"Content"`
	Path    string `json:"Path"`
}

// ---------------------------------------------------------------------------
// 上传（单文件 + 同步，D39）
// ---------------------------------------------------------------------------

// UploadRequest 是 `POST /api/upload` 的请求体。
type UploadRequest struct {
	// Path 本地文件绝对路径。
	Path string `json:"Path"`
	// Uploader 目标图床；省略则用 agent 侧当前激活配置。
	Uploader *UploadTarget `json:"Uploader,omitempty"`
	// JobUID / Seq 仅用于日志与事件归属。
	JobUID string `json:"JobUID,omitempty"`
	Seq    int    `json:"Seq,omitempty"`
	// FileName 可选：直接用这个名字（覆盖模板结果）。
	FileName string `json:"FileName,omitempty"`

	// 魔法路径模板（D43/D70），来自 StorageConfigs。
	PathTemplate string `json:"PathTemplate,omitempty"`
	FileTemplate string `json:"FileTemplate,omitempty"`
	// UserUID 供模板变量 {uid} 使用。
	UserUID string `json:"UserUID,omitempty"`
	// SupportsPathTemplate 驱动是否支持自定义远端路径；false 时降级为文件名前缀。
	SupportsPathTemplate *bool `json:"SupportsPathTemplate,omitempty"`
}

// UploadTarget 指定「用哪个驱动的哪个配置」。
type UploadTarget struct {
	Type       string `json:"Type"`
	ConfigName string `json:"ConfigName,omitempty"`
}

// UploadData 是一次成功上传的结果。
type UploadData struct {
	Seq         int    `json:"Seq"`
	URL         string `json:"URL"`
	ThumbURL    string `json:"ThumbURL"`
	FileName    string `json:"FileName"`
	Extname     string `json:"Extname"`
	Width       int    `json:"Width"`
	Height      int    `json:"Height"`
	Size        int64  `json:"Size"`
	ContentType string `json:"ContentType"`
	// UploaderType 实际使用的驱动类型（agent 可能因降级而改变）。
	UploaderType string `json:"UploaderType"`
	// Raw 是完整的 picgo IImgInfo，**含插件回写字段**（如 github 的 sha）。
	//
	// ⚠️ Go 侧**必须**把它原样存进 UploadResults.RawOutput，否则这些字段丢失后
	// **再也无法删除远端文件**（D47）。
	Raw RawImgInfo `json:"Raw"`
}

// UploadErrorData 是上传失败时的 `Data`。
//
// agent 契约：**上传失败不返回 5xx**，而是 `Code = "ERR_PICGO"` + HTTP 200，
// 便于 Go 逐项记录失败原因（其他项继续跑）。
type UploadErrorData struct {
	Seq   int    `json:"Seq"`
	Error string `json:"Error"`
	// Raw 通常为 null；保留字段以便将来带上部分结果。
	Raw RawImgInfo `json:"Raw"`
}

// ---------------------------------------------------------------------------
// 远端删除（D47）
// ---------------------------------------------------------------------------

// RemoveRequest 是 `POST /api/delete` 的请求体。
type RemoveRequest struct {
	UploaderType string `json:"UploaderType,omitempty"`
	// Items 是上传时的完整 IImgInfo（原样，字段名保持 picgo 的）。
	Items []RawImgInfo `json:"Items"`
}

// RemoveData 是远端删除的结果。
type RemoveData struct {
	// RemoteDeleted 是否真的删掉了远端文件。
	RemoteDeleted bool `json:"RemoteDeleted"`
	// Supported 该驱动是否支持远端删除。
	// false 时 Go 侧只删本地记录，并在审计日志里说明（D47）。
	Supported bool   `json:"Supported"`
	Message   string `json:"Message"`
}

// ---------------------------------------------------------------------------
// 任务与日志
// ---------------------------------------------------------------------------

// JobInfo 是 agent 侧的 job（**只维护执行期状态**；持久化真相源在 Go 侧）。
type JobInfo struct {
	UID        string         `json:"UID"`
	Kind       string         `json:"Kind"`
	Status     string         `json:"Status"`
	Progress   int            `json:"Progress"`
	Payload    map[string]any `json:"Payload"`
	Result     map[string]any `json:"Result"`
	Error      string         `json:"Error"`
	CreatedAt  int64          `json:"CreatedAt"`
	StartedAt  int64          `json:"StartedAt"`
	FinishedAt int64          `json:"FinishedAt"`
}

// JobsData 是 `GET /api/jobs` 的响应。
type JobsData struct {
	Jobs []JobInfo `json:"Jobs"`
}

// LogsData 是 `GET /api/logs` 的响应（picgo.log 尾部）。
type LogsData struct {
	Path  string   `json:"Path"`
	Lines []string `json:"Lines"`
	Total int      `json:"Total"`
}

// JobCreated 是插件安装类端点的响应（异步任务）。
type JobCreated struct {
	JobUID string `json:"JobUID"`
}

// ---------------------------------------------------------------------------
// 输入参数
// ---------------------------------------------------------------------------

// CreateUploaderConfigInput 是 `POST /api/uploaders/configs` 的入参。
type CreateUploaderConfigInput struct {
	Type       string
	ConfigName string
	// Config 的键是**驱动字段名**（原样）。
	Config RawConfig
	// Activate 是否顺带切换为当前激活配置。
	Activate bool
}

// TestUploaderInput 是 `POST /api/uploaders/test` 的入参。
//
// 两种模式：
//   - 传 Config：**不落盘**直接测（用于新建表单）
//   - 只传 ConfigName：测已保存的配置
type TestUploaderInput struct {
	Type       string
	ConfigName string
	Config     RawConfig
	Answers    map[string]any
}

// PatchConfigInput 是 `PATCH /api/config` 的入参（点路径合并）。
type PatchConfigInput struct {
	Patch map[string]any
}
