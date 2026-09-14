package lsky

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// ---------------------------------------------------------------------------
// 信封与错误码映射（docs/API.md §12.2）
// ---------------------------------------------------------------------------

func TestEnvelopeShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	OK(c, map[string]any{"foo": "bar"})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", w.Code)
	}

	// 必须是 snake_case 的小写信封，且 status 是 bool
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}

	if _, ok := got["status"].(bool); !ok {
		t.Errorf("status 必须是 bool，实际 %#v", got["status"])
	}
	if got["status"] != true {
		t.Errorf("成功响应 status 应为 true，实际 %#v", got["status"])
	}
	if got["message"] != MessageSuccess {
		t.Errorf("message 应为 %q，实际 %#v", MessageSuccess, got["message"])
	}
	// 内部信封的键不应出现
	for _, k := range []string{"Code", "Message", "Data"} {
		if _, exists := got[k]; exists {
			t.Errorf("不应出现内部信封的键 %q（这是 Lsky 契约，字段必须 snake_case）", k)
		}
	}
}

func TestEnvelopeFailureShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	FailMsg(c, 40102, "Unauthenticated.")

	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got["status"] != false {
		t.Errorf("失败响应 status 应为 false，实际 %#v", got["status"])
	}
	if got["data"] != nil {
		t.Errorf("失败响应 data 应为 null，实际 %#v", got["data"])
	}
	if got["message"] != "Unauthenticated." {
		t.Errorf("message 不符，实际 %#v", got["message"])
	}
}

// TestErrorCodeMapping 校验 §12.2 的映射表（含 40001 → 422 这条易错项）。
func TestErrorCodeMapping(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		wantStatus int
		wantMsg    string
	}{
		{"凭据错误", 40101, 401, "These credentials do not match our records."},
		{"未认证", 40102, 401, "Unauthenticated."},
		{"令牌过期", 40103, 401, "Unauthenticated."},
		{"账号禁用", 40104, 403, "Account disabled."},
		{"权限不足", 40301, 403, "Forbidden."},
		{"配额不足", 40302, 403, "Insufficient storage capacity."},
		{"不存在", 40401, 404, "Not Found."},
		{"冲突", 40901, 409, "Conflict."},
		{"限流", 42901, 429, "Too Many Requests."},
		// ⚠️ 40001 映射到 422（Laravel 校验失败语义），不是 400
		{"参数错误", 40001, 422, "The given data was invalid."},
		{"内部错误", 50001, 500, "Server Error."},
		{"内核不可用", 50002, 503, "Service Unavailable."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			Fail(c, response.Code(tc.code))

			if w.Code != tc.wantStatus {
				t.Errorf("HTTP 状态码应为 %d，实际 %d", tc.wantStatus, w.Code)
			}
			var got map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &got)
			if got["message"] != tc.wantMsg {
				t.Errorf("message 应为 %q，实际 %#v", tc.wantMsg, got["message"])
			}
		})
	}
}

// TestFailMsgNeverLeaksEmptyMessage 校验空文案会用映射后的默认值。
func TestFailMsgNeverLeaksEmptyMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Fail(c, 40401)

	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if s, _ := got["message"].(string); strings.TrimSpace(s) == "" {
		t.Error("message 不应为空（空文案会落到映射后的默认值）")
	}
}

// ---------------------------------------------------------------------------
// 分页：Laravel 形状（双重 data）
// ---------------------------------------------------------------------------

func TestPageEnvelopeIsLaravelShaped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	items := []map[string]any{{"key": "up_1"}}
	OKPage(c, items, 128, 1, 20)

	var got struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Data        []map[string]any `json:"data"`
			CurrentPage int              `json:"current_page"`
			LastPage    int              `json:"last_page"`
			PerPage     int              `json:"per_page"`
			Total       int64            `json:"total"`
			From        int              `json:"from"`
			To          int              `json:"to"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析失败: %v\n%s", err, w.Body.String())
	}

	// ⚠️ 关键：data.data 是「双重 data」（Laravel 分页器形状），客户端依赖它
	if len(got.Data.Data) != 1 {
		t.Errorf("data.data 应有 1 项，实际 %d", len(got.Data.Data))
	}
	if got.Data.CurrentPage != 1 {
		t.Errorf("current_page 应为 1，实际 %d", got.Data.CurrentPage)
	}
	// 128 / 20 → 7 页
	if got.Data.LastPage != 7 {
		t.Errorf("last_page 应为 7，实际 %d", got.Data.LastPage)
	}
	if got.Data.PerPage != 20 {
		t.Errorf("per_page 应为 20，实际 %d", got.Data.PerPage)
	}
	if got.Data.Total != 128 {
		t.Errorf("total 应为 128，实际 %d", got.Data.Total)
	}
	if got.Data.From != 1 || got.Data.To != 20 {
		t.Errorf("from/to 应为 1/20，实际 %d/%d", got.Data.From, got.Data.To)
	}
}

func TestPageEnvelopeEmptyAndBoundaries(t *testing.T) {
	cases := []struct {
		name             string
		total            int64
		page, perPage    int
		wantItems        int
		wantLastPage     int
		wantFrom, wantTo int
	}{
		{"空结果", 0, 1, 20, 0, 0, 0, 0},
		{"恰好一页", 20, 1, 20, 0, 1, 1, 20},
		{"最后一页不满", 25, 2, 20, 0, 2, 21, 25},
		{"超范围页", 5, 3, 20, 0, 1, 0, 0}, // 页码超出范围 → from/to 都是 0
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			var items []any
			if tc.wantItems > 0 {
				items = make([]any, tc.wantItems)
			}
			OKPage(c, items, tc.total, tc.page, tc.perPage)

			var got struct {
				Data struct {
					Data     []any `json:"data"`
					LastPage int   `json:"last_page"`
					From     int   `json:"from"`
					To       int   `json:"to"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got.Data.LastPage != tc.wantLastPage {
				t.Errorf("last_page 应为 %d，实际 %d", tc.wantLastPage, got.Data.LastPage)
			}
			if got.Data.From != tc.wantFrom || got.Data.To != tc.wantTo {
				t.Errorf("from/to 应为 %d/%d，实际 %d/%d", tc.wantFrom, tc.wantTo, got.Data.From, got.Data.To)
			}
		})
	}
}

// TestPageEnvelopeNilItemsIsEmptyArray 校验空结果返回 `[]` 而不是 `null`。
func TestPageEnvelopeNilItemsIsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	OKPage(c, nil, 0, 1, 20)

	body := w.Body.String()
	if strings.Contains(body, `"data":null`) {
		t.Errorf("空结果时 data.data 应为 [] 而不是 null（Laravel 分页器行为）：\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// 时间格式与 links 派生
// ---------------------------------------------------------------------------

func TestFormatTimeIsLskyLayout(t *testing.T) {
	// 2026-02-14 09:30:15 UTC+8 → 用本地时区解析以确保断言稳定
	// 这里不依赖具体时区，只校验格式
	cases := []struct {
		in   int64
		want string
	}{
		{0, ""},
		{-1, ""},
		{1767225600, ""}, // 占位，下面单独校验非空
	}

	for _, tc := range cases[:2] {
		if got := formatTime(tc.in); got != tc.want {
			t.Errorf("formatTime(%d) 应为 %q，实际 %q", tc.in, tc.want, got)
		}
	}

	got := formatTime(1767225600)
	if got == "" {
		t.Fatal("非零时间不应为空")
	}
	// 必须是 `Y-m-d H:i:s`（19 字符，两个冒号，一个空格）
	if len(got) != 19 {
		t.Errorf("时间字符串长度应为 19，实际 %d（%q）", len(got), got)
	}
	if strings.Count(got, ":") != 2 {
		t.Errorf("时间字符串应含两个冒号，实际 %q", got)
	}
	if strings.Count(got, "-") != 2 {
		t.Errorf("时间字符串应含两个短横线，实际 %q", got)
	}
	if strings.ContainsAny(got, "TZ") {
		t.Errorf("不应是 RFC3339 格式，实际 %q", got)
	}
}

func TestBuildLinksAllSixForms(t *testing.T) {
	links := buildLinks("a.png", "https://cdn/x/a.png", "")

	if links.URL != "https://cdn/x/a.png" {
		t.Errorf("url 不符: %q", links.URL)
	}
	if links.HTML != `<img src="https://cdn/x/a.png" alt="a.png" />` {
		t.Errorf("html 不符: %q", links.HTML)
	}
	if links.Markdown != "![a.png](https://cdn/x/a.png)" {
		t.Errorf("markdown 不符: %q", links.Markdown)
	}
	if links.BBCode != "[img]https://cdn/x/a.png[/img]" {
		t.Errorf("bbcode 不符: %q", links.BBCode)
	}
	if links.MarkdownWithLink != "[![a.png](https://cdn/x/a.png)](https://cdn/x/a.png)" {
		t.Errorf("markdown_with_link 不符: %q", links.MarkdownWithLink)
	}
	// 本项目不做缩略图（D84），因此恒为空
	if links.ThumbnailURL != "" {
		t.Errorf("thumbnail_url 应为空（D84 不做缩略图），实际 %q", links.ThumbnailURL)
	}
}

func TestBuildLinksEmptyURL(t *testing.T) {
	links := buildLinks("a.png", "", "")
	if links.URL != "" || links.HTML != "" || links.Markdown != "" ||
		links.BBCode != "" || links.MarkdownWithLink != "" {
		t.Errorf("url 为空时应全部为空，实际 %+v", links)
	}
}

func TestBuildLinksFallbackName(t *testing.T) {
	// 文件名为空时用 "image" 兜底，避免产出 `![](...)`
	links := buildLinks("", "https://cdn/x.png", "")
	if !strings.Contains(links.Markdown, "image") {
		t.Errorf("空文件名应有兜底名，实际 %q", links.Markdown)
	}
}

func TestThumbURLPassthrough(t *testing.T) {
	links := buildLinks("a.png", "https://cdn/a.png", "https://cdn/a.thumb.png")
	if links.ThumbnailURL != "https://cdn/a.thumb.png" {
		t.Errorf("thumbURL 应原样透传，实际 %q", links.ThumbnailURL)
	}
}

// ---------------------------------------------------------------------------
// 排序参数映射
// ---------------------------------------------------------------------------

func TestParseLskyOrder(t *testing.T) {
	cases := []struct {
		in        string
		wantSort  string
		wantOrder string
	}{
		{"newest", "createdAt", "desc"},
		{"NEWEST", "createdAt", "desc"},
		{"oldest", "createdAt", "asc"},
		{"OLDEST", "createdAt", "asc"},
		{"", "createdAt", "desc"},          // 缺省
		{"bogus", "createdAt", "desc"},     // 未知值回退 newest
		{"  oldest  ", "createdAt", "asc"}, // 容忍空白
	}

	for _, tc := range cases {
		gotSort, gotOrder := parseLskyOrder(tc.in)
		if gotSort != tc.wantSort || gotOrder != tc.wantOrder {
			t.Errorf("parseLskyOrder(%q) = (%q,%q)，期望 (%q,%q)",
				tc.in, gotSort, gotOrder, tc.wantSort, tc.wantOrder)
		}
	}
}

func TestAtoiDefault(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 7, 7},    // 空 → 默认值
		{"abc", 7, 7}, // 非数字 → 默认值
		{"0", 7, 0},   // 合法 0
		{"3", 7, 3},
		{"  12 ", 7, 12}, // 容忍空白
		{"-5", 7, -5},
	}

	for _, tc := range cases {
		if got := atoiDefault(tc.in, tc.def); got != tc.want {
			t.Errorf("atoiDefault(%q, %d) = %d，期望 %d", tc.in, tc.def, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 路由冲突检测（D80）
// ---------------------------------------------------------------------------

func TestDetectConflictsAllowsKnownLskyPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()

	g := e.Group("/api/v1")
	g.POST("/tokens", func(*gin.Context) {})
	g.DELETE("/tokens", func(*gin.Context) {})
	g.GET("/profile", func(*gin.Context) {})
	g.GET("/strategies", func(*gin.Context) {})
	g.POST("/upload", func(*gin.Context) {})
	g.GET("/images", func(*gin.Context) {})
	g.DELETE("/images/:key", func(*gin.Context) {})
	g.GET("/albums", func(*gin.Context) {})
	g.DELETE("/albums/:id", func(*gin.Context) {})

	if conflicts := DetectConflicts(e); len(conflicts) != 0 {
		t.Errorf("Lsky 自身路径不应被判为冲突，实际 %d 处: %v", len(conflicts), conflicts)
	}
}

func TestDetectConflictsFindsIntruder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()

	// 合法 Lsky 路由
	e.POST("/api/v1/upload", func(*gin.Context) {})
	// 闯入者：内部接口误挂到保留区
	e.GET("/api/v1/internal-thing", func(*gin.Context) {})
	e.POST("/api/v1/images/extra", func(*gin.Context) {})

	conflicts := DetectConflicts(e)
	if len(conflicts) != 2 {
		t.Fatalf("应检测到 2 处冲突，实际 %d: %v", len(conflicts), conflicts)
	}

	// 结果应排序（便于一次修完）
	paths := []string{conflicts[0].Path, conflicts[1].Path}
	if !sort.StringsAreSorted(paths) {
		t.Errorf("冲突列表应排序，实际 %v", paths)
	}

	msg := conflicts[0].Error()
	if !strings.Contains(msg, "/api/web/v1") {
		t.Errorf("错误提示应指明正确前缀，实际: %s", msg)
	}
}

func TestDetectConflictsIgnoresInternalAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()

	// 内部 API 前缀完全不涉及保留区
	e.GET("/api/web/v1/uploads", func(*gin.Context) {})
	e.POST("/api/web/v1/storage/configs", func(*gin.Context) {})
	// 合法的 Lsky 路由（POST upload，不是 GET）
	e.POST("/api/v1/upload", func(*gin.Context) {})

	if conflicts := DetectConflicts(e); len(conflicts) != 0 {
		t.Errorf("内部 API 前缀与合法 Lsky 路由不应被判为冲突，实际 %v", conflicts)
	}
}

// TestDetectConflictsFlagsWrongMethodOnReservedPath 校验「方法不对」也算冲突。
//
// 场景：Lsky 契约是 `POST /api/v1/upload`，若有人注册 `GET /api/v1/upload`，
// 虽然方法不同不覆盖，但它**落在保留区**，语义上仍是风险（将来可能与
// 契约演进撞车），因此同样报冲突。
func TestDetectConflictsFlagsWrongMethodOnReservedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/api/v1/upload", func(*gin.Context) {})

	if conflicts := DetectConflicts(e); len(conflicts) != 1 {
		t.Errorf("保留区内的非契约路径应被标记，实际 %d 处: %v", len(conflicts), conflicts)
	}
}

func TestVerifyContractReportsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	// 只注册一半
	e.POST("/api/v1/tokens", func(*gin.Context) {})
	e.GET("/api/v1/profile", func(*gin.Context) {})

	missing := VerifyContract(e)
	if len(missing) == 0 {
		t.Fatal("应报告缺失的契约路径")
	}
	if !sort.StringsAreSorted(missing) {
		t.Errorf("缺失列表应排序，实际 %v", missing)
	}
	// 已注册的两条不应出现
	for _, m := range missing {
		if m == "POST /api/v1/tokens" || m == "GET /api/v1/profile" {
			t.Errorf("已注册的路径不应出现在缺失列表: %s", m)
		}
	}
}

func TestVerifyContractAllRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()

	// 手工注册契约的 9 条路径（VerifyContract 只看路由表，不调用 handler）
	g := e.Group("/api/v1")
	g.POST("/tokens", func(*gin.Context) {})
	g.DELETE("/tokens", func(*gin.Context) {})
	g.GET("/profile", func(*gin.Context) {})
	g.GET("/strategies", func(*gin.Context) {})
	g.POST("/upload", func(*gin.Context) {})
	g.GET("/images", func(*gin.Context) {})
	g.DELETE("/images/:key", func(*gin.Context) {})
	g.GET("/albums", func(*gin.Context) {})
	g.DELETE("/albums/:id", func(*gin.Context) {})

	missing := VerifyContract(e)
	if len(missing) != 0 {
		t.Errorf("全部注册后不应有缺失，实际 %v", missing)
	}
}

// ---------------------------------------------------------------------------
// 测试助手
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 鉴权失败必须用 Lsky 信封（**实测踩到的 bug**）
// ---------------------------------------------------------------------------

// TestAbortLskyUsesLskyEnvelope 校验鉴权失败时返回的是 Lsky 信封，
// 而不是内部信封 `{Code, Message, Data}`。
//
// 为什么重要：第三方客户端按 `status` 字段判断成功与否。
// 若鉴权失败返回内部信封，客户端拿不到 `status`，会表现为
// 「登录成功但上传报未知错误」这种极难排查的现象。
func TestAbortLskyUsesLskyEnvelope(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		wantStatus int
		wantMsg    string
	}{
		{"未认证", 40102, 401, "Unauthenticated."},
		{"令牌过期", 40103, 401, "Unauthenticated."},
		{"账号禁用", 40104, 403, "Account disabled."},
		{"内部错误", 50001, 500, "Server Error."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			abortLsky(c, response.Code(tc.code))

			if w.Code != tc.wantStatus {
				t.Errorf("HTTP 应为 %d，实际 %d", tc.wantStatus, w.Code)
			}
			if !c.IsAborted() {
				t.Error("应中断后续 handler")
			}

			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			if got["status"] != false {
				t.Errorf("status 应为 false，实际 %#v", got["status"])
			}
			if got["message"] != tc.wantMsg {
				t.Errorf("message 应为 %q，实际 %#v", tc.wantMsg, got["message"])
			}
			// 内部信封的标志字段绝不能出现
			for _, k := range []string{"Code", "Message", "Data"} {
				if _, exists := got[k]; exists {
					t.Errorf("不应出现内部信封字段 %q", k)
				}
			}
		})
	}
}

// TestRequireAPITokenRejectsNonAPIToken 校验 Lsky 层只认 API Token。
//
// 内部 JWT 与 Cookie 都不应被接受：Lsky 客户端是程序，
// 让浏览器的登录态影响 API 语义会产生难以排查的隐式行为。
func TestRequireAPITokenRejectsNonAPIToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	mw := h.requireAPIToken()

	cases := []struct {
		name   string
		setup  func(c *gin.Context)
		reason string
	}{
		{
			name:   "无 Authorization",
			setup:  func(c *gin.Context) {},
			reason: "无凭据",
		},
		{
			name: "JWT（非 pcw_ 前缀）",
			setup: func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.fake.jwt")
			},
			reason: "Lsky 层不认内部 JWT",
		},
		{
			name: "Cookie（浏览器登录态）",
			setup: func(c *gin.Context) {
				c.Request.AddCookie(&http.Cookie{Name: "pcw_at", Value: "something"})
			},
			reason: "Lsky 层不认 Cookie",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
			tc.setup(c)

			mw(c)

			// 无 TokenRepo → 走到取令牌就应已中断（不会 panic）
			if !c.IsAborted() {
				t.Fatalf("应拒绝（%s）", tc.reason)
			}
			if w.Code != http.StatusUnauthorized {
				t.Errorf("HTTP 应为 401，实际 %d", w.Code)
			}
		})
	}
}
