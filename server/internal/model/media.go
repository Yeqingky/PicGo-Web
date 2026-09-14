package model

// 上传状态与来源取值。
const (
	UploadStatusPending = "pending"
	UploadStatusSuccess = "success"
	UploadStatusFailed  = "failed"

	UploadSourceWeb  = "web"
	UploadSourceAPI  = "api"
	UploadSourceLsky = "lsky"
)

// Upload 是图片元数据（热点表）。
//
// ⚠️ 注意：
//   - **没有 `(StorageUID, SHA256)` 唯一索引** —— D66 已取消内容去重
//   - **没有可见性列** —— D33 刻意设计：图片归属完全靠 UserUID 过滤，
//     图片本身的公开性由图床（PicGo 驱动）决定，本项目无权也无法控制。
//     **不要后续添加 visibility 列。**
type Upload struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	UID          string `gorm:"size:32;uniqueIndex;not null"` // up_ 前缀的 ULID
	UserUID      string `gorm:"size:32;index;not null"`
	StorageUID   string `gorm:"size:32;index;not null"` // 引用 StorageConfigs.UID
	AlbumUID     string `gorm:"size:32;index"`          // 空 = 不属于任何相册

	FileName     string `gorm:"size:255;not null"` // 最终文件名（含扩展名）
	OriginalName string `gorm:"size:255"`          // 原始上传文件名
	AliasName    string `gorm:"size:255"`          // 用户重命名（展示优先用 alias）

	Size      int64  `gorm:"not null;default:0"` // 字节
	MimeType  string `gorm:"size:127"`
	Extension string `gorm:"size:32;index"`
	Width     int    `gorm:"not null;default:0"`
	Height    int    `gorm:"not null;default:0"`
	SHA256    string `gorm:"size:64;index"` // 仅记录，不建唯一索引（D66 不去重）

	URL      string `gorm:"size:1024"`
	ThumbURL string `gorm:"size:1024"` // 预留：本项目不做缩略图（D84），前端**不得依赖**

	Status string `gorm:"size:16;index;not null;default:pending"` // pending|success|failed
	Error  string `gorm:"type:text"`
	Source string `gorm:"size:16;not null;default:web"` // web | api | lsky
	JobUID string `gorm:"size:32;index"`                // 归属批次

	Metadata  string `gorm:"type:text"`
	CreatedAt int64  `gorm:"not null;index"`
	UpdatedAt int64  `gorm:"not null"`
}

func (Upload) TableName() string { return "Uploads" }

// DisplayName 返回展示用文件名（优先用户重命名）。
func (u *Upload) DisplayName() string {
	if u.AliasName != "" {
		return u.AliasName
	}
	if u.OriginalName != "" {
		return u.OriginalName
	}
	return u.FileName
}

// UploadResult 保存上传返回的**完整 output 对象**（与 Upload 1:1）。
//
// 拆表原因（D78 体量不同）：picgo 的 IImgInfo 完整对象可能很大，
// 且**只在删除远端文件时才需要**（D47 要求把插件回写的 sha 等字段留住）。
// 热点表 Uploads 不带这个字段，列表查询更轻。
//
// ⚠️ RawOutput 里的字段名是 **picgo-core 的 IImgInfo 定义**（fileName / imgUrl /
// extname / sha …），**一律保持原样、不适用 D81 大驼峰**（D81.3 第 5 条）：
// 删除远端文件时要把它原封不动交回插件（remove 事件），字段名被改过插件就认不出来了。
type UploadResult struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UploadUID string `gorm:"size:32;uniqueIndex;not null"`
	RawOutput string `gorm:"type:text;not null"` // JSON：完整 IImgInfo[]
	FilePath  string `gorm:"size:512"`           // 本地暂存路径（若仍存在）
	CreatedAt int64  `gorm:"not null"`
}

func (UploadResult) TableName() string { return "UploadResults" }

// Album 是相册（用于整理图片）。
type Album struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	UID            string `gorm:"size:32;uniqueIndex;not null"`
	UserUID        string `gorm:"size:32;index;not null"`
	ParentUID      string `gorm:"size:32;index"` // 预留给未来嵌套；当前恒为空
	Name           string `gorm:"size:128;not null"`
	Intro          string `gorm:"size:512"`
	CoverUploadUID string `gorm:"size:32"`
	ImageCount     int64  `gorm:"not null;default:0"` // 冗余计数
	SortOrder      int    `gorm:"not null;default:0"`
	Metadata       string `gorm:"type:text"`
	CreatedAt      int64  `gorm:"not null"`
	UpdatedAt      int64  `gorm:"not null"`
}

func (Album) TableName() string { return "Albums" }
