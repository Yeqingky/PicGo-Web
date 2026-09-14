package model

// Job 类型（Kind）。
const (
	JobKindUpload          = "upload"
	JobKindPluginInstall   = "plugin.install"
	JobKindPluginUninstall = "plugin.uninstall"
	JobKindPluginUpdate    = "plugin.update"
	JobKindConfigSync      = "config.sync"
	JobKindThemeInstall    = "theme.install"
)

// Job 状态。**只有 4 个，不要 partial**（D37）：
// 只要有 item 失败即为 failed，成功项的 URL 照样在 Result 里回传。
const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
)

// Job 是一次批次任务（D36：一次请求 = 1 个 job，含 N 个文件）。
//
// **driver 信息只在 job 层**（D38 一批一驱动），JobItems 不重复存。
type Job struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	UID        string `gorm:"size:32;uniqueIndex;not null"`  // job_ 前缀
	Kind       string `gorm:"size:32;index;not null"`        // upload|plugin.install|...
	Status     string `gorm:"size:16;index;not null"` // queued|running|succeeded|failed
	Progress   int    `gorm:"not null;default:0"`            // 0..100

	UserUID    string `gorm:"size:32;index"` // 发起者（系统任务为空）
	StorageUID string `gorm:"size:32;index"` // 一批一驱动（D38）

	TotalItems     int `gorm:"not null;default:0"`
	SucceededItems int `gorm:"not null;default:0"`
	FailedItems    int `gorm:"not null;default:0"`
	SkippedItems   int `gorm:"not null;default:0"` // D66 取消去重后恒为 0，保留字段符合 D77

	Payload string `gorm:"type:text"` // JSON 请求上下文
	Result  string `gorm:"type:text"` // JSON 汇总结果
	Error   string `gorm:"type:text"`

	CreatedAt  int64 `gorm:"not null;index"`
	StartedAt  int64 `gorm:"not null;default:0"`
	FinishedAt int64 `gorm:"not null;default:0"`
}

func (Job) TableName() string { return "Jobs" }

// Finished 判断是否已终结。
func (j *Job) Finished() bool {
	return j.Status == JobStatusSucceeded || j.Status == JobStatusFailed
}

// JobItem 是批次内的单个文件。
//
// **并发限制作用在 item 层**（D36）。
type JobItem struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	JobUID    string `gorm:"size:32;not null"`
	Seq       int    `gorm:"not null"` // 批次内顺序，保证展示稳定
	UploadUID string `gorm:"size:32;index"`
	FileName  string `gorm:"size:255"`
	Status    string `gorm:"size:16;index;not null"` // queued|running|succeeded|failed
	Attempts  int    `gorm:"not null;default:0"`
	Error     string `gorm:"type:text"`

	StartedAt  int64 `gorm:"not null;default:0"`
	FinishedAt int64 `gorm:"not null;default:0"`
}

func (JobItem) TableName() string { return "JobItems" }

// JobLog 是任务的逐行执行日志。
//
// 拆表原因（D78 生命周期不同）：任务日志是逐行输出（npm install、上传过程），
// 体量大、写完很快就不再需要，与 OperationLogs（审计、保留 180 天）完全不同。
type JobLog struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	JobUID    string `gorm:"size:32;not null"`
	Seq       int    `gorm:"not null"`
	Line      string `gorm:"type:text"`
	CreatedAt int64  `gorm:"not null"`
}

func (JobLog) TableName() string { return "JobLogs" }
