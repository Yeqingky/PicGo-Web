package lsky

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// ReservedPaths 是 Lsky 契约**独占**的路径（docs/DECISIONS.md D80）。
//
// ⚠️ 这些是**外部冻结契约**：第三方客户端把路径硬编码在代码里
// （实测 `picgo-plugin-lankong` 等全部用字符串拼接 `${server}/api/v1/upload`），
// 因此内部 API **永不占用**。新增内部接口一律挂 `/api/web/v1/**`。
var ReservedPaths = []string{
	"/api/v1/tokens",
	"/api/v1/profile",
	"/api/v1/strategies",
	"/api/v1/upload",
	"/api/v1/images",
	"/api/v1/albums",
}

// knownLskyPaths 是**本包注册的全部路径**（用于冲突检测的白名单）。
//
// 与 ReservedPaths 的区别：后者是「保留区」的 6 个前缀，前者是实际注册的具体路径
// （含参数段）。检测时用前者判断「这条路由是不是我们自己注册的」。
var knownLskyPaths = map[string]bool{
	"POST /api/v1/tokens":        true,
	"DELETE /api/v1/tokens":      true,
	"GET /api/v1/profile":        true,
	"GET /api/v1/strategies":     true,
	"POST /api/v1/upload":        true,
	"GET /api/v1/images":         true,
	"DELETE /api/v1/images/:key": true,
	"GET /api/v1/albums":         true,
	"DELETE /api/v1/albums/:id":  true,
}

// ConflictError 描述一次路由冲突。
type ConflictError struct {
	// Method / Path 是「闯入」的路由。
	Method string
	Path   string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf(
		"%s %s 落在 Lsky 保留区 /api/v1 之下，但不是 Lsky 契约的已知路径。\n"+
			"      → 内部 API 必须挂 /api/web/v1/**（D80）；\n"+
			"      → Lsky 保留集是外部冻结契约：%s",
		e.Method, e.Path, strings.Join(ReservedPaths, " / "),
	)
}

// DetectConflicts 检查已注册路由是否**误闯** Lsky 保留区。
//
// # 规则
//
// `/api/v1/**` 下的路由只允许两种：
//  1. 本包注册的 Lsky 契约路径（knownLskyPaths）
//  2. 本包的路由中间件挂载点（`/api/v1` 本身，见 Register 里的 Group）
//
// 其余一律视为冲突 —— 典型场景是**将来有人新增内部接口时手滑写成 `/api/v1/xxx`**。
// gin 对重复路径会静默覆盖或并存，导致第三方客户端行为诡异且极难排查，
// 因此在启动时 fail fast。
//
// 返回全部冲突（排序后，便于一次修完）。
func DetectConflicts(engine *gin.Engine) []*ConflictError {
	var conflicts []*ConflictError

	for _, route := range engine.Routes() {
		if !inReservedZone(route.Path) {
			continue
		}
		if knownLskyPaths[route.Method+" "+route.Path] {
			continue
		}
		// 允许 `/api/v1` 本身（路由组的挂载点通常不产生路由，但保守起见留着）
		if route.Path == "/api/v1" {
			continue
		}
		conflicts = append(conflicts, &ConflictError{Method: route.Method, Path: route.Path})
	}

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Path != conflicts[j].Path {
			return conflicts[i].Path < conflicts[j].Path
		}
		return conflicts[i].Method < conflicts[j].Method
	})
	return conflicts
}

// VerifyContract 校验 Lsky 的 9 条契约路径是否**全部注册成功**。
//
// 用途：启动自检。若某条漏注册（例如将来重构时删了路由），
// 第三方客户端的对应功能会**静默失效**（调用方只看到 404），
// 因此主动检查比等用户报障好。
//
// 返回缺失的路径（已排序）。
func VerifyContract(engine *gin.Engine) []string {
	registered := make(map[string]bool)
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	var missing []string
	for key := range knownLskyPaths {
		if !registered[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// inReservedZone 判断路径是否落在 Lsky 保留区（`/api/v1` 本身或其子路径）。
func inReservedZone(p string) bool {
	if p == "/api/v1" {
		return true
	}
	return strings.HasPrefix(p, "/api/v1/")
}
