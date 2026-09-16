package lsky

import (
	"fmt"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// lskyTimeLayout 是 lsky/Laravel 的时间格式 `Y-m-d H:i:s`。
//
// 内部一律 Unix 秒（int64），对外转字符串 —— 这是契约要求的差异（docs/API.md §12.1）。
const lskyTimeLayout = "2006-01-02 15:04:05"

// formatTime 把 Unix 秒格式化为 lsky 时间字符串；0 返回空串。
func formatTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format(lskyTimeLayout)
}

// buildLinks 派生 lsky 的六种外链。
//
// 前三种是项目原生支持的（D68）；后三种为契约完整性而拼装：
//   - bbcode:            `[img]<url>[/img]`
//   - markdown_with_link:`[![name](url)](url)`
//   - thumbnail_url:     无缩略图（D84 不做缩略图）→ 空串
func buildLinks(displayName, url, thumbURL string) Links {
	if url == "" {
		return Links{}
	}
	name := displayName
	if name == "" {
		name = "image"
	}

	return Links{
		URL:      url,
		HTML:     fmt.Sprintf(`<img src="%s" alt="%s" />`, url, name),
		Markdown: fmt.Sprintf("![%s](%s)", name, url),
		BBCode:   fmt.Sprintf("[img]%s[/img]", url),
		// 带链接的 Markdown：点击图片跳到图片本身
		MarkdownWithLink: fmt.Sprintf("[![%s](%s)](%s)", name, url, url),
		ThumbnailURL:     thumbURL,
	}
}

// toImageData 把内部视图转成 lsky 的图片表示。
//
// `pathname` 是**远端路径**：本项目不解析也不保证其结构（由驱动决定），
// 因此从 URL 反推最靠后的部分作为展示用 pathname；完全无法推断时退化为文件名。
//
// 之所以不直接存远端路径：URL 形态由图床决定（D66 的核心理由），
// 我们只存驱动返回的 URL 字符串，不猜不拼。
func toImageData(v *service.UploadView) ImageData {
	if v == nil {
		return ImageData{}
	}

	displayName := firstNonEmpty(v.AliasName, v.OriginalName, v.FileName)

	return ImageData{
		Key:        v.UID,
		Name:       v.FileName,
		OriginName: firstNonEmpty(v.OriginalName, v.FileName),
		Pathname:   derivePathname(v),
		Size:       v.Size,
		Mimetype:   v.MimeType,
		Extension:  strings.TrimPrefix(v.Extension, "."),
		Width:      v.Width,
		Height:     v.Height,
		URL:        v.URL,
		Links:      buildLinks(displayName, v.URL, v.ThumbURL),
		CreatedAt:  formatTime(v.CreatedAt),
		UpdatedAt:  formatTime(v.UpdatedAt),
	}
}

// derivePathname 从 URL 推断展示用的 pathname。
//
// 只用于**展示**：lsky 客户端会把它显示在图片列表里。
// 取 URL path 的最后两段（如 `img/2026/a.png` → `2026/a.png`）过于武断，
// 因此直接取整个 path（去首斜杠）——客户端拿到的路径与真实图床一致。
func derivePathname(v *service.UploadView) string {
	url := strings.TrimSpace(v.URL)
	if url != "" {
		if i := strings.Index(url, "://"); i >= 0 {
			rest := url[i+3:]
			if j := strings.Index(rest, "/"); j >= 0 {
				p := rest[j+1:]
				if k := strings.IndexAny(p, "?#"); k >= 0 {
					p = p[:k]
				}
				if p != "" {
					return p
				}
			}
		}
	}
	// 退化：用文件名（至少让客户端有东西显示）
	return firstNonEmpty(v.FileName, v.OriginalName)
}

// toProfileData 组装用户资料。
//
// 字节与 KB 两套字段都给（见 dto.go ProfileData 的说明）。
// AlbumNum 恒为 0：本项目已移除相册功能，仅保留契约字段。
func toProfileData(u *model.User, nickname string, imageNum int64) ProfileData {
	kb := func(b int64) int64 {
		if b <= 0 {
			return 0
		}
		// lsky 的 capacity/useCapacity 单位是 KB，向下取整（与 lsky 行为一致）
		return b / 1024
	}

	return ProfileData{
		ID:     u.UID,
		Email:  u.Email,
		Name:   firstNonEmpty(nickname, u.Email),
		Avatar: "",

		CapacityBytes: u.CapacityBytes,
		UsedBytes:     u.UsedBytes,
		ImageNum:      imageNum,
		AlbumNum:      0,

		Capacity:    kb(u.CapacityBytes),
		UseCapacity: kb(u.UsedBytes),
	}
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseLskyOrder 把 lsky 的 `order` 参数映射为内部的 Sort/Order。
//
// lsky 只提供 `newest` / `oldest` 两个值（见 docs/API.md §12.3）。
func parseLskyOrder(order string) (sortField, direction string) {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "oldest":
		return "createdAt", "asc"
	default: // newest 或未指定
		return "createdAt", "desc"
	}
}
