package lsky

// 本文件定义 Lsky 契约的 DTO。
//
// ⚠️ **所有字段名一律 snake_case**，与 lsky-pro 一致（外部冻结契约，D81.3 第 1 条）。
// 这与项目内部「JSON 字段用 PascalCase」的规范是**刻意冲突**的：
// 对第三方客户端而言，字段名就是 API 契约本身，改名即破坏兼容。

// ---------------------------------------------------------------------------
// 令牌
// ---------------------------------------------------------------------------

// TokenRequest 是 `POST /api/v1/tokens` 的请求体。
type TokenRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// TokenData 是换取令牌的响应体。
type TokenData struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// ---------------------------------------------------------------------------
// 用户资料
// ---------------------------------------------------------------------------

// ProfileData 是 `GET /api/v1/profile` 的响应体。
//
// 同时给出**字节**（本项目口径，精确）与 **KB**（lsky 口径，兼容）：
// lsky 的 `capacity` / `useCapacity` 单位是 KB，客户端按 KB 显示容量条；
// 而我们的 `Users.CapacityBytes` 是字节。两套都返回是最稳妥的兼容做法。
type ProfileData struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`

	// 字节（本项目口径）
	CapacityBytes int64 `json:"capacityBytes"`
	UsedBytes     int64 `json:"usedBytes"`

	ImageNum int64 `json:"imageNum"`
	AlbumNum int64 `json:"albumNum"`

	// KB（lsky 口径，兼容旧客户端）
	Capacity    int64 `json:"capacity"`
	UseCapacity int64 `json:"useCapacity"`
}

// ---------------------------------------------------------------------------
// 存储策略
// ---------------------------------------------------------------------------

// StrategyData 是 `GET /api/v1/strategies` 的数组项。
//
// `id` 是本项目的 `StorageUID`（**字符串，不是数字**）；
// 客户端会把 `strategy_id` 原样回传，因此 upload 端点需接受字符串 id。
type StrategyData struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Intro   string `json:"intro"`
	Key     string `json:"key"`
	Default bool   `json:"default"`
}

// ---------------------------------------------------------------------------
// 图片
// ---------------------------------------------------------------------------

// Links 是 lsky 的六种外链形式。
//
// 本项目原生支持三种（`url` / `html` / `markdown`，D68）；
// `bbcode` / `markdown_with_link` / `thumbnail_url` 为**契约完整性**而派生：
// 前两者由 url 简单拼装，`thumbnail_url` 在无缩略图时返回 `""`（本项目不做缩略图，D84）。
type Links struct {
	URL              string `json:"url"`
	HTML             string `json:"html"`
	Markdown         string `json:"markdown"`
	BBCode           string `json:"bbcode"`
	MarkdownWithLink string `json:"markdown_with_link"`
	ThumbnailURL     string `json:"thumbnail_url"`
}

// ImageData 是单张图片的表示（上传响应 / 列表项共用）。
type ImageData struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	OriginName string `json:"origin_name"`
	Pathname   string `json:"pathname"`
	Size       int64  `json:"size"`
	Mimetype   string `json:"mimetype"`
	Extension  string `json:"extension"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	MD5        string `json:"md5,omitempty"`
	SHA1       string `json:"sha1,omitempty"`

	URL   string `json:"url"`
	Links Links  `json:"links"`

	// 时间字段是**字符串** `Y-m-d H:i:s`（lsky/Laravel 口径），
	// 而内部一律用 Unix 秒 —— 不是笔误，是刻意的契约差异（docs/API.md §12.1）。
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}
