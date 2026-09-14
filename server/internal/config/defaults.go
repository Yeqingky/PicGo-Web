package config

// 本文件是 D18 的**第 3 层：代码默认值**。
//
// 读取优先级（settings 包实现）：数据库值 → 本表默认值 → 类型零值。
// 新增配置键时**只改本文件 + 数据库写入**，**不需要迁移**（D77）。
//
// ⚠️ 配置键命名保持 `dot.lowerCamel`，**不受 D81（PascalCase）影响**（D81.3 第 3 条）：
// 它们是 KV 表的字符串 key，不是列名。
//
// ⚠️ **主题特有配置**（如默认主题的 BackgroundURL / HomepageFeatures…）**不在本表**：
// 它们由各主题的 manifest.Configuration.Items 声明（D95/D98），存 ThemeConfigs 表。
// 本表只保留 `theme.active`（站点级选择，不属于任何主题的 schema）。

// Category 是配置键的分类，用于后台设置页分 Tab 展示。
type Category string

const (
	CategorySite        Category = "site"
	CategoryTheme       Category = "theme"
	CategoryUser        Category = "user"
	CategoryUpload      Category = "upload"
	CategoryMail        Category = "mail"
	CategoryOAuth       Category = "oauth"
	CategorySecurity    Category = "security"
	CategoryLog         Category = "log"
	CategoryPicgo       Category = "picgo"
	CategoryIntegration Category = "integration"
)

// ValueType 决定前端渲染什么控件，以及如何序列化到 Value 列。
type ValueType string

const (
	TypeString ValueType = "string"
	TypeInt    ValueType = "int"
	TypeBool   ValueType = "bool"
	TypeJSON   ValueType = "json"
	TypeSecret ValueType = "secret" // 加密存储，API 响应一律掩码
)

// Key 是一条配置键的元数据。
type Key struct {
	Name        string    // 键名（dot.lowerCamel）
	Category    Category  // 分类
	Type        ValueType // 值类型
	Default     any       // 代码默认值（第 3 层兜底）
	Description string    // 后台展示的说明
}

// Keys 是全部**站点级**配置键的注册表，顺序即后台展示顺序。
//
// 加新键：直接在对应分组的末尾追加一行即可，**不需要数据库迁移**。
var Keys = []Key{
	// ---------- 站点 ----------
	{Name: "site.name", Category: CategorySite, Type: TypeString, Default: "PicGo Web",
		Description: "站点名称"},
	{Name: "site.subtitle", Category: CategorySite, Type: TypeString, Default: "",
		Description: "副标题（顶栏与首页）"},
	{Name: "site.baseUrl", Category: CategorySite, Type: TypeString, Default: "",
		Description: "对外访问地址，用于拼接 OAuth 回调地址等；留空则按请求推断"},
	{Name: "site.description", Category: CategorySite, Type: TypeString, Default: "",
		Description: "站点描述（meta description）"},
	{Name: "site.keywords", Category: CategorySite, Type: TypeString, Default: "",
		Description: "站点关键词（meta keywords，逗号分隔）"},
	{Name: "site.notice", Category: CategorySite, Type: TypeString, Default: "",
		Description: "站点公告（支持 Markdown），留空则不展示"},
	{Name: "site.icp", Category: CategorySite, Type: TypeString, Default: "",
		Description: "备案号，展示在页脚"},
	{Name: "site.iconUrl", Category: CategorySite, Type: TypeString, Default: "",
		Description: "站点图标 URL；留空则使用当前主题的 assets/favicon.ico"},

	// ---------- 主题（仅站点级选择，主题自身的配置在 ThemeConfigs） ----------
	{Name: "theme.active", Category: CategoryTheme, Type: TypeString, Default: "default",
		Description: "当前启用的主题 ID"},

	// ---------- 用户 ----------
	{Name: "user.defaultCapacityBytes", Category: CategoryUser, Type: TypeInt, Default: int64(5) << 30,
		Description: "新建用户的默认存储配额（字节）；只影响此后新建的用户，不回溯修改已有用户"},
	{Name: "user.unlimitedCapacity", Category: CategoryUser, Type: TypeBool, Default: false,
		Description: "为真时新建用户默认不限额（CapacityBytes = 0）；只影响此后新建的用户"},
	{Name: "user.defaultStatus", Category: CategoryUser, Type: TypeString, Default: "active",
		Description: "新建用户的默认状态：active | disabled"},
	{Name: "user.allowSelfRegistration", Category: CategoryUser, Type: TypeBool, Default: false,
		Description: "自助注册开关。⚠️ 预留开关：本项目明确不做自助注册流程（D25），实现时不要为它写注册逻辑"},

	// ---------- 上传 ----------
	{Name: "upload.maxSizeBytes", Category: CategoryUpload, Type: TypeInt, Default: int64(20) << 20,
		Description: "单文件大小上限（字节）"},
	{Name: "upload.allowedExts", Category: CategoryUpload, Type: TypeJSON,
		Default:     []string{"jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico", "avif"},
		Description: "允许上传的扩展名白名单（小写，不含点）"},
	{Name: "upload.blockSvg", Category: CategoryUpload, Type: TypeBool, Default: false,
		Description: "禁止上传 SVG（SVG 可内嵌脚本，按需开启）"},
	{Name: "upload.concurrency", Category: CategoryUpload, Type: TypeInt, Default: 1,
		Description: "上传队列并发度。1 = 严格串行（不依赖 PicGo-Core 补丁）；>1 需补丁支持按次指定图床"},
	{Name: "upload.retryTimes", Category: CategoryUpload, Type: TypeInt, Default: 1,
		Description: "单个文件失败后的重试次数"},
	{Name: "upload.retryBackoffMs", Category: CategoryUpload, Type: TypeInt, Default: 2000,
		Description: "重试退避基数（毫秒），按指数增长"},
	{Name: "upload.queueMaxLength", Category: CategoryUpload, Type: TypeInt, Default: 1000,
		Description: "等待队列长度上限，超出返回 42901"},
	{Name: "upload.itemTimeoutSeconds", Category: CategoryUpload, Type: TypeInt, Default: 300,
		Description: "单个文件上传超时（秒）"},
	{Name: "upload.shutdownGraceSeconds", Category: CategoryUpload, Type: TypeInt, Default: 30,
		Description: "优雅关闭时等待在途上传完成的时长（秒）"},
	{Name: "upload.defaultAlbumUID", Category: CategoryUpload, Type: TypeString, Default: "",
		Description: "新上传默认归入的相册 UID；留空表示不归入相册"},
	{Name: "upload.rateLimit.enabled", Category: CategoryUpload, Type: TypeBool, Default: false,
		Description: "上传限流总开关。**默认禁用**（D73）"},
	{Name: "upload.rateLimit.perHour", Category: CategoryUpload, Type: TypeInt, Default: 100,
		Description: "每用户每小时最多上传张数（仅 enabled = true 时生效）"},
	{Name: "upload.rateLimit.perDay", Category: CategoryUpload, Type: TypeInt, Default: 500,
		Description: "每用户每天最多上传张数（仅 enabled = true 时生效）"},
	{Name: "upload.rateLimit.action", Category: CategoryUpload, Type: TypeString, Default: "reject",
		Description: "超限行为：reject（拒绝，返回 42901）| log（仅记录不拦截）"},
	{Name: "upload.keepLocalCopy", Category: CategoryUpload, Type: TypeBool, Default: false,
		Description: "上传成功后是否保留本地暂存文件"},
	{Name: "upload.keepLocalDays", Category: CategoryUpload, Type: TypeInt, Default: 7,
		Description: "本地暂存文件的保留天数；keepLocalCopy 为假时忽略"},

	// ---------- 邮件 ----------
	{Name: "mail.enabled", Category: CategoryMail, Type: TypeBool, Default: false,
		Description: "邮件功能总开关"},
	{Name: "mail.host", Category: CategoryMail, Type: TypeString, Default: "",
		Description: "SMTP 服务器地址"},
	{Name: "mail.port", Category: CategoryMail, Type: TypeInt, Default: 465,
		Description: "SMTP 端口"},
	{Name: "mail.encryption", Category: CategoryMail, Type: TypeString, Default: "ssl",
		Description: "加密方式：ssl | starttls | none"},
	{Name: "mail.username", Category: CategoryMail, Type: TypeString, Default: "",
		Description: "SMTP 用户名"},
	{Name: "mail.password", Category: CategoryMail, Type: TypeSecret, Default: "",
		Description: "SMTP 密码（加密存储，接口返回掩码）"},
	{Name: "mail.fromAddress", Category: CategoryMail, Type: TypeString, Default: "",
		Description: "发件人地址"},
	{Name: "mail.fromName", Category: CategoryMail, Type: TypeString, Default: "PicGo Web",
		Description: "发件人显示名"},

	// ---------- OAuth ----------
	{Name: "oauth.github.enabled", Category: CategoryOAuth, Type: TypeBool, Default: false,
		Description: "是否在登录页展示 GitHub 登录按钮（D26：只接 GitHub）"},
	{Name: "oauth.github.clientId", Category: CategoryOAuth, Type: TypeString, Default: "",
		Description: "GitHub OAuth App 的 Client ID"},
	{Name: "oauth.github.clientSecret", Category: CategoryOAuth, Type: TypeSecret, Default: "",
		Description: "GitHub OAuth App 的 Client Secret（加密存储）"},
	{Name: "oauth.autoBindByEmail", Category: CategoryOAuth, Type: TypeBool, Default: false,
		Description: "GitHub 邮箱与站内账号相同时自动绑定；默认关闭（更安全，需先登录后在个人设置里绑定）"},

	// ---------- 安全 ----------
	{Name: "security.sessionTtlHours", Category: CategorySecurity, Type: TypeInt, Default: 168,
		Description: "refresh token 有效期（小时），默认 7 天"},
	{Name: "security.accessTokenTtlMinutes", Category: CategorySecurity, Type: TypeInt, Default: 15,
		Description: "access token（JWT）有效期（分钟）"},
	{Name: "security.loginMaxAttempts", Category: CategorySecurity, Type: TypeInt, Default: 5,
		Description: "登录失败次数阈值，超过则临时锁定"},
	{Name: "security.loginWindowMinutes", Category: CategorySecurity, Type: TypeInt, Default: 5,
		Description: "登录失败计数的时间窗（分钟）"},

	// ---------- 日志 ----------
	{Name: "log.retentionDays", Category: CategoryLog, Type: TypeInt, Default: 180,
		Description: "操作日志保留天数；0 表示永久保留"},
	{Name: "log.jobRetentionDays", Category: CategoryLog, Type: TypeInt, Default: 7,
		Description: "任务执行日志（JobLogs）保留天数；0 表示永久保留"},

	// ---------- PicGo ----------
	{Name: "picgo.npmRegistry", Category: CategoryPicgo, Type: TypeString, Default: "https://registry.npmmirror.com",
		Description: "插件安装使用的 npm 源"},
	{Name: "picgo.npmProxy", Category: CategoryPicgo, Type: TypeString, Default: "",
		Description: "npm 代理地址"},
	{Name: "picgo.uploadProxy", Category: CategoryPicgo, Type: TypeString, Default: "",
		Description: "上传时使用的代理地址"},
	{Name: "picgo.transformer", Category: CategoryPicgo, Type: TypeString, Default: "path",
		Description: "全局 transformer，通常保持 path"},
	{Name: "picgo.configPath", Category: CategoryPicgo, Type: TypeString, Default: "",
		Description: "picgo config.json 的路径；留空则使用 <dataDir>/picgo/config.json"},

	// ---------- 对外集成 ----------
	{Name: "integration.lsky.enabled", Category: CategoryIntegration, Type: TypeBool, Default: true,
		Description: "是否挂载 Lsky v1 兼容层（/api/v1/**），供第三方图床客户端接入（D52）"},
	{Name: "integration.lsky.deleteRemoteOnDelete", Category: CategoryIntegration, Type: TypeBool, Default: false,
		Description: "Lsky 的 DELETE /images/{key} 是否同时删除图床上的远端文件（该契约本身无此参数，用开关表达）"},
	{Name: "integration.lsky.tokenTtlDays", Category: CategoryIntegration, Type: TypeInt, Default: 365,
		Description: "Lsky token 有效期（天）"},
}

// keyIndex 是 Name → Key 的索引，供 settings 包快速查默认值与元数据。
var keyIndex = func() map[string]Key {
	m := make(map[string]Key, len(Keys))
	for _, k := range Keys {
		m[k.Name] = k
	}
	return m
}()

// Lookup 按键名查询元数据。
func Lookup(name string) (Key, bool) {
	k, ok := keyIndex[name]
	return k, ok
}

// DefaultOf 返回键的代码默认值；键未注册时返回 (nil, false)。
func DefaultOf(name string) (any, bool) {
	k, ok := keyIndex[name]
	if !ok {
		return nil, false
	}
	return k.Default, true
}

// KeysOf 返回某分类下的全部键（保持注册顺序）。
func KeysOf(c Category) []Key {
	var out []Key
	for _, k := range Keys {
		if k.Category == c {
			out = append(out, k)
		}
	}
	return out
}

// AllCategories 返回全部分类（按注册顺序去重），供后台渲染 Tab。
func AllCategories() []Category {
	seen := make(map[Category]bool)
	var out []Category
	for _, k := range Keys {
		if !seen[k.Category] {
			seen[k.Category] = true
			out = append(out, k.Category)
		}
	}
	return out
}
