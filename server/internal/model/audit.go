package model

// 操作日志状态。
const (
	LogStatusSuccess = "success"
	LogStatusFailed  = "failed"
)

// 操作日志类型（Type）。取值是**字符串枚举**，新增取值不需要 DDL（D77）。
//
// 清单见 docs/DECISIONS.md D45。新增类型时只需在此追加常量 +
// 在后台的类型清单接口里暴露。
const (
	LogTypeUpload          = "upload"         // 上传成功/失败
	LogTypeImageDelete     = "image.delete"   // 删除图片
	LogTypeImageUpdate     = "image.update"   // 重命名/移动相册
	LogTypeMailSend        = "mail.send"      // 邮件发送
	LogTypeUserCreate      = "user.create"    // 账号创建
	LogTypeUserDelete      = "user.delete"    // 账号注销
	LogTypeUserUpdate      = "user.update"    // 修改配额/状态/密码/角色
	LogTypeStorageCreate   = "storage.create" // 新建存储配置
	LogTypeStorageUpdate   = "storage.update" // 修改存储配置
	LogTypeStorageDelete   = "storage.delete" // 删除存储配置
	LogTypePluginInstall   = "plugin.install"
	LogTypePluginUninstall = "plugin.uninstall"
	LogTypePluginUpdate    = "plugin.update"
	LogTypeAuthLogin       = "auth.login"  // 登录成功
	LogTypeAuthFailed      = "auth.failed" // 登录失败
	LogTypeAuthLogout      = "auth.logout"
	LogTypeSettingUpdate   = "setting.update"
	LogTypeLogCleanup      = "system.log.cleanup"
	// 主题（D96）
	LogTypeThemeInstall        = "theme.install"
	LogTypeThemeUninstall      = "theme.uninstall"
	LogTypeThemeActivate       = "theme.activate"
	LogTypeThemeRescan         = "theme.rescan"
	LogTypeThemeSettingsUpdate = "theme.settings.update"
	LogTypeThemeSettingsClear  = "theme.settings.clear"
	LogTypeThemeError          = "theme.error"
)

// LogTypeItem 是可过滤的类型清单（供后台 GET /logs/types）。
type LogTypeItem struct {
	Type        string `json:"Type"`
	Description string `json:"Description"`
}

// LogTypes 返回全部可过滤的日志类型（顺序即后台展示顺序）。
func LogTypes() []LogTypeItem {
	return []LogTypeItem{
		{Type: LogTypeUpload, Description: "上传"},
		{Type: LogTypeImageDelete, Description: "删除图片"},
		{Type: LogTypeImageUpdate, Description: "修改图片"},
		{Type: LogTypeMailSend, Description: "邮件发送"},
		{Type: LogTypeUserCreate, Description: "账号创建"},
		{Type: LogTypeUserDelete, Description: "账号注销"},
		{Type: LogTypeUserUpdate, Description: "账号修改"},
		{Type: LogTypeStorageCreate, Description: "新建存储配置"},
		{Type: LogTypeStorageUpdate, Description: "修改存储配置"},
		{Type: LogTypeStorageDelete, Description: "删除存储配置"},
		{Type: LogTypePluginInstall, Description: "安装插件"},
		{Type: LogTypePluginUninstall, Description: "卸载插件"},
		{Type: LogTypePluginUpdate, Description: "更新插件"},
		{Type: LogTypeAuthLogin, Description: "登录成功"},
		{Type: LogTypeAuthFailed, Description: "登录失败"},
		{Type: LogTypeAuthLogout, Description: "登出"},
		{Type: LogTypeSettingUpdate, Description: "修改系统设置"},
		{Type: LogTypeLogCleanup, Description: "日志清理"},
		{Type: LogTypeThemeInstall, Description: "安装主题"},
		{Type: LogTypeThemeUninstall, Description: "卸载主题"},
		{Type: LogTypeThemeActivate, Description: "启用主题"},
		{Type: LogTypeThemeRescan, Description: "重新扫描主题"},
		{Type: LogTypeThemeSettingsUpdate, Description: "修改主题设置"},
		{Type: LogTypeThemeSettingsClear, Description: "清理主题设置"},
		{Type: LogTypeThemeError, Description: "主题错误"},
	}
}

// OperationLog 是统一操作日志（D45）。
//
// 与 Jobs / JobLogs 的区别：Jobs 是**任务执行过程**（含实时进度与逐行日志）；
// OperationLogs 是**审计级结果记录**（一次操作一条，只记结果与关键上下文）。
type OperationLog struct {
	ID     uint64 `gorm:"primaryKey;autoIncrement"`
	UID    string `gorm:"size:32;uniqueIndex;not null"` // log_ 前缀 ULID
	Type   string `gorm:"size:64;index;not null"`
	Status string `gorm:"size:16;index;not null"` // success | failed

	UserUID    string `gorm:"size:32;index"` // 操作者（系统操作为空）
	Username   string `gorm:"size:255"`      // 冗余，便于展示与搜索
	TargetType string `gorm:"size:32;index"`
	TargetUID  string `gorm:"size:32;index"`

	Detail    string `gorm:"type:text"` // JSON：成功时的上下文
	Error     string `gorm:"type:text"` // 失败原因（失败时必填）
	ClientIP  string `gorm:"size:64"`
	UserAgent string `gorm:"size:255"`
	CreatedAt int64  `gorm:"not null;index"`
}

func (OperationLog) TableName() string { return "OperationLogs" }

// 邮件模板名（EmailLogs.Template）。
const (
	MailTemplateInvite        = "invite"
	MailTemplateResetPassword = "reset_password"
	MailTemplateTest          = "test"
	MailTemplateVerify        = "verify"
)

// EmailLog 是邮件发送记录。
//
// ⚠️ **不存邮件正文**（用户明确要求）。
type EmailLog struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	UID            string `gorm:"size:32;uniqueIndex;not null"`
	ToAddress      string `gorm:"size:255;index;not null"`
	Subject        string `gorm:"size:255"`
	Template       string `gorm:"size:64;index"` // invite | reset_password | ...
	Status         string `gorm:"size:16;index;not null"`
	Error          string `gorm:"type:text"`
	RelatedUserUID string `gorm:"size:32;index"`
	CreatedAt      int64  `gorm:"not null;index"`
}

func (EmailLog) TableName() string { return "EmailLogs" }
