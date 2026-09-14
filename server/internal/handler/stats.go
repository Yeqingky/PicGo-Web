package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// StatsDeps 是统计端点的依赖。
type StatsDeps struct {
	DB    *gorm.DB
	Users *repository.UserRepo
	Log   *slog.Logger
}

// StatsHandler 处理 `GET /api/web/v1/system/stats`（`docs/API.md` §10）。
//
// 权限：需登录；**管理员看全局，普通用户只看自己**（与图库 `Scope` 同样的语义）。
type StatsHandler struct {
	deps StatsDeps
}

// NewStatsHandler 构造。
func NewStatsHandler(deps StatsDeps) *StatsHandler { return &StatsHandler{deps: deps} }

// Register 挂载（`api` 已带 `/api/web/v1` 前缀且已 apply 登录中间件）。
func (h *StatsHandler) Register(api *gin.RouterGroup) {
	api.GET("/system/stats", h.Get)
}

// Get 处理 `GET /system/stats`。
//
// 返回仪表盘所需的聚合数据。**管理员**额外拿到用户数与按存储分组；
// 普通用户只拿到自己的上传统计（不泄露他人信息）。
func (h *StatsHandler) Get(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		response.Fail(c, response.CodeUnauthorized)
		return
	}
	isAdmin := viewer.IsAdmin()
	scope := "mine"
	if isAdmin {
		scope = "all"
	}

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	// ---- 上传统计（按 scope 过滤）----
	uploads := gin.H{}
	var uploadsTotal, uploadsSize, todayCount, pendingCount, failedCount int64

	q := func() *gorm.DB {
		tx := h.deps.DB.Model(&model.Upload{})
		if !isAdmin {
			tx = tx.Where("UserUID = ?", viewer.UID)
		}
		return tx
	}

	var err error
	if err = q().Count(&uploadsTotal).Error; err != nil {
		h.fail(c, "统计上传总数失败", err)
		return
	}
	// COALESCE 使空表返回 0 而不是 NULL
	if err = q().Select("COALESCE(SUM(size), 0)").Scan(&uploadsSize).Error; err != nil {
		h.fail(c, "统计上传体积失败", err)
		return
	}
	if err = q().Where("CreatedAt >= ?", dayStart).Count(&todayCount).Error; err != nil {
		h.fail(c, "统计今日上传失败", err)
		return
	}
	if err = q().Where("Status = ?", model.UploadStatusPending).Count(&pendingCount).Error; err != nil {
		h.fail(c, "统计待上传失败", err)
		return
	}
	if err = q().Where("Status = ?", model.UploadStatusFailed).Count(&failedCount).Error; err != nil {
		h.fail(c, "统计失败上传失败", err)
		return
	}

	uploads["Total"] = uploadsTotal
	uploads["TotalSize"] = uploadsSize
	uploads["TodayCount"] = todayCount
	uploads["PendingCount"] = pendingCount
	uploads["FailedCount"] = failedCount

	// ---- 任务统计（进行中 / 排队中）----
	jobs := gin.H{"Running": int64(0), "Queued": int64(0)}
	var running, queued int64
	jq := func() *gorm.DB {
		tx := h.deps.DB.Model(&model.Job{})
		if !isAdmin {
			tx = tx.Where("UserUID = ?", viewer.UID)
		}
		return tx
	}
	if err = jq().Where("Status = ?", model.JobStatusRunning).Count(&running).Error; err != nil {
		h.fail(c, "统计运行中任务失败", err)
		return
	}
	if err = jq().Where("Status = ?", model.JobStatusQueued).Count(&queued).Error; err != nil {
		h.fail(c, "统计排队任务失败", err)
		return
	}
	jobs["Running"] = running
	jobs["Queued"] = queued

	// ---- 30 天趋势 ----
	trend, err := h.trend(viewer, isAdmin, now)
	if err != nil {
		h.fail(c, "统计趋势失败", err)
		return
	}

	body := gin.H{
		"Scope":   scope,
		"Uploads": uploads,
		"Jobs":    jobs,
		"Trend":   trend,
	}

	// ---- 管理员专属：用户统计 + 按存储分组 ----
	if isAdmin {
		total, err := h.deps.Users.Count()
		if err != nil {
			h.fail(c, "统计用户总数失败", err)
			return
		}
		admins, err := h.deps.Users.CountAdmins()
		if err != nil {
			h.fail(c, "统计管理员数失败", err)
			return
		}
		var disabled int64
		if err = h.deps.DB.Model(&model.User{}).
			Where("Status = ?", model.UserStatusDisabled).
			Count(&disabled).Error; err != nil {
			h.fail(c, "统计禁用用户失败", err)
			return
		}
		body["Users"] = gin.H{
			"Total":    total,
			"Active":   total - disabled,
			"Disabled": disabled,
			"Admins":   admins,
		}

		byStorage, err := h.byStorage()
		if err != nil {
			h.fail(c, "统计存储分布失败", err)
			return
		}
		body["ByStorage"] = byStorage
	}

	response.OK(c, body)
}

// trend 返回最近 30 天每天的上传数与体积。
//
// 实现说明：一次聚合查询后在 Go 侧补齐空缺日期（SQL 做「补零日期序列」需要
// 递归 CTE，两方言写法不同且可读性差）。30 天数据量极小，补零成本可忽略。
func (h *StatsHandler) trend(viewer *model.User, isAdmin bool, now time.Time) ([]gin.H, error) {
	const days = 30

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := dayStart.AddDate(0, 0, -(days - 1))

	type row struct {
		Day   int64
		Count int64
		Size  int64
	}
	var rows []row

	// created_at 是 Unix 秒；按「本地日」分组需要按天取整。
	// 这里用 `created_at - (created_at % 86400)` 会按 UTC 切分，而用户期望本地日。
	// 因此传入「本地时区相对 UTC 的偏移秒数」，在 SQL 里先位移再取整。
	_, offset := dayStart.Zone()
	tx := h.deps.DB.Model(&model.Upload{}).
		Select("(CreatedAt + ?) / 86400 * 86400 - ? AS Day, COUNT(*) AS Count, COALESCE(SUM(Size),0) AS Size", offset, offset).
		Where("CreatedAt >= ?", from.Unix())
	if !isAdmin {
		tx = tx.Where("UserUID = ?", viewer.UID)
	}
	if err := tx.Group("Day").Order("Day ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	byDay := make(map[int64]row, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}

	out := make([]gin.H, 0, days)
	for i := 0; i < days; i++ {
		d := dayStart.AddDate(0, 0, -(days - 1 - i))
		key := d.Unix()
		r := byDay[key]
		out = append(out, gin.H{
			"Date":  d.Format("2006-01-02"),
			"Count": r.Count,
			"Size":  r.Size,
		})
	}
	return out, nil
}

// byStorage 按存储配置分组统计图片数（仅管理员可见）。
func (h *StatsHandler) byStorage() ([]gin.H, error) {
	type row struct {
		StorageUID string
		Count      int64
	}
	var rows []row
	if err := h.deps.DB.Model(&model.Upload{}).
		Select("StorageUID, COUNT(*) AS Count").
		Group("StorageUID").
		Order("Count DESC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	// 补上存储配置的展示名（存储可能已被删除，此时 Name 为空）
	var configs []model.StorageConfig
	if err := h.deps.DB.Select("UID", "Name").Find(&configs).Error; err != nil {
		return nil, err
	}
	nameOf := make(map[string]string, len(configs))
	for _, cfg := range configs {
		nameOf[cfg.UID] = cfg.Name
	}

	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{
			"StorageUID": r.StorageUID,
			"Name":       nameOf[r.StorageUID],
			"Count":      r.Count,
		})
	}
	return out, nil
}

func (h *StatsHandler) fail(c *gin.Context, msg string, err error) {
	h.deps.Log.Error(msg, "err", err)
	response.Fail(c, response.CodeInternal)
}
