package model

// StorageConfig 是存储配置的**元数据**（不含任何密钥）。
//
// 同一驱动类型可有**多条**实例（D64）：多个 WebDAV、多个 GitHub 仓库。
// 后端用 UID 作为唯一标识，前端显示 Name（D64）。
//
// 密钥拆到 StorageSecrets（D78 敏感级不同）：任何针对本表的列表/统计查询
// 都**不可能误带出密钥**。
type StorageConfig struct {
	ID              uint64 `gorm:"primaryKey;autoIncrement"`
	UID             string `gorm:"size:32;uniqueIndex;not null"`     // st_ 前缀的 ULID
	Name            string `gorm:"size:64;index;not null"`           // 展示名（全局唯一，应用层校验）
	Type            string `gorm:"size:64;not null"`                 // 驱动类型：github / webdav / s3 / ...
	PicgoConfigName string `gorm:"size:64;not null;default:Default"` // 映射 picgo _configName（D65）
	Enabled         bool   `gorm:"not null;default:true"`
	IsDefault       bool   `gorm:"not null;default:false"` // 全局同时只有一条为 true
	PathTemplate    string `gorm:"size:255"`               // 魔法路径（D43，每配置独立）
	FileTemplate    string `gorm:"size:255"`               // 魔法文件名
	Capabilities    string `gorm:"type:text"`              // JSON：运行时探测的驱动能力（D77：不硬编码驱动名）
	Metadata        string `gorm:"type:text"`              // JSON 扩展位
	CreatedAt       int64  `gorm:"not null"`
	UpdatedAt       int64  `gorm:"not null"`
}

func (StorageConfig) TableName() string { return "StorageConfigs" }

// StorageSecret 是存储驱动的凭据（与 StorageConfig 1:1，AES-256-GCM 加密）。
type StorageSecret struct {
	ID               uint64 `gorm:"primaryKey;autoIncrement"`
	StorageUID       string `gorm:"size:32;uniqueIndex;not null"`
	EncryptedPayload string `gorm:"type:text;not null"` // AES-256-GCM(JSON)，含 token/secret/password
	KeyVersion       int    `gorm:"not null;default:1"` // 主密钥轮换支持
	CreatedAt        int64  `gorm:"not null"`
	UpdatedAt        int64  `gorm:"not null"`
}

func (StorageSecret) TableName() string { return "StorageSecrets" }

// ThemeConfig 是**主题的配置项**（D95），每行一个键。
//
// 独立表而非复用 SystemSettings 的原因（D78 拆表原则）：
//   - 生命周期不同：随主题的安装/卸载而生死
//   - 一对多关系：一个主题 → 多个配置键
//   - 可审计：每行带 UpdatedBy / UpdatedAt，能回答「谁在何时改了哪个键」
//
// 键集合由主题 manifest.Configuration.Items 声明，**Go 侧不硬编码**（D95/D98）。
// theme.active 不在此表 —— 它是站点级选择，存 SystemSettings。
type ThemeConfig struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	ThemeID   string `gorm:"size:64;not null"`                // 主题 ID（如 default）
	Key       string `gorm:"size:128;not null"`               // 配置项名（如 BackgroundURL）
	Value     string `gorm:"type:text"`                       // JSON 编码的值
	ValueType string `gorm:"size:16;not null;default:string"` // 与 manifest 的 Type 对应
	UpdatedBy string `gorm:"size:32"`                         // 操作者 UserUID
	CreatedAt int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (ThemeConfig) TableName() string { return "ThemeConfigs" }
