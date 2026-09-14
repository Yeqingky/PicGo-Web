package model

// SystemSetting 是站点级配置（KV）。
//
// 用 KV 表而不是列的原因（D77）：新增配置项**不需要迁移**。
// 键位与默认值见 internal/config/defaults.go（D18 第 3 层）。
//
// ⚠️ 主题特有配置**不在此表**，而在 ThemeConfigs（D95）；
// 只有 theme.active 这一个站点级主题选择留在这里。
type SystemSetting struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	Key       string `gorm:"size:128;uniqueIndex;not null"`
	Value     string `gorm:"type:text"` // JSON 编码；Encrypted 为真时是 AES 密文
	ValueType string `gorm:"size:16;not null;default:string"`
	Encrypted bool   `gorm:"not null;default:false"`
	Category  string `gorm:"size:32;index"`
	UpdatedBy string `gorm:"size:32"` // 操作者 UserUID
	CreatedAt int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (SystemSetting) TableName() string { return "SystemSettings" }

// UserSetting 是用户级偏好（KV）。
type UserSetting struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UserUID   string `gorm:"size:32;not null"`
	Key       string `gorm:"size:128;not null"`
	Value     string `gorm:"type:text"`
	ValueType string `gorm:"size:16;not null;default:string"`
	CreatedAt int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (UserSetting) TableName() string { return "UserSettings" }

// Plugin 是插件的**展示缓存**。
//
// npm 包的真实状态在 node_modules（agent 是真相源），此表只做缓存，
// 避免每次读盘。刷新时机：启动、插件任务成功后、GET /plugins?refresh=true。
type Plugin struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"size:191;uniqueIndex;not null"` // picgo-plugin-x / @s/x
	Version     string `gorm:"size:32"`
	Description string `gorm:"type:text"`
	Author      string `gorm:"size:191"`
	Homepage    string `gorm:"size:512"`
	Uploader    string `gorm:"size:64"`
	Transformer string `gorm:"size:64"`
	Enabled     bool   `gorm:"not null;default:true"`
	GuiOnly     bool   `gorm:"not null;default:false"` // 含 guiMenu/commands，Web 端不可用
	Metadata    string `gorm:"type:text"`
	InstalledAt int64  `gorm:"not null"`
	UpdatedAt   int64  `gorm:"not null"`
}

func (Plugin) TableName() string { return "Plugins" }
