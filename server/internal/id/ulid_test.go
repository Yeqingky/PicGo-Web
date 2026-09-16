package id

import (
	"strings"
	"sync"
	"testing"
)

func TestNewProducesValidULID(t *testing.T) {
	s := New()
	if len(s) != 26 {
		t.Fatalf("ULID 长度应为 26，实际 %d（%s）", len(s), s)
	}
	if !Valid(s) {
		t.Errorf("生成的 ULID 无法被解析: %s", s)
	}
	// Crockford Base32 不含 I L O U
	for _, ch := range s {
		if strings.ContainsRune("ILOUilou", ch) {
			t.Errorf("ULID 含非法字符 %q: %s", ch, s)
		}
	}
}

func TestWithPrefixAndHelpers(t *testing.T) {
	cases := []struct {
		name   string
		got    string
		prefix string
	}{
		{"Upload", Upload(), PrefixUpload},
		{"Storage", Storage(), PrefixStorage},
		{"Job", Job(), PrefixJob},
		{"User", User(), PrefixUser},
		{"APIToken", APIToken(), PrefixAPIToken},
		{"RefreshToken", RefreshToken(), PrefixRefreshToken},
		{"OAuthIdentity", OAuthIdentity(), PrefixOAuth},
		{"Log", Log(), PrefixLog},
		{"EmailLog", EmailLog(), PrefixEmail},
		{"Theme", Theme(), PrefixTheme},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.got, tc.prefix) {
				t.Errorf("缺少前缀 %q: %s", tc.prefix, tc.got)
			}
			body := strings.TrimPrefix(tc.got, tc.prefix)
			if len(body) != 26 {
				t.Errorf("前缀后应为 26 字符 ULID，实际 %d: %s", len(body), tc.got)
			}
			if !Valid(body) {
				t.Errorf("前缀后的部分不是合法 ULID: %s", tc.got)
			}
		})
	}
}

func TestUniqueness(t *testing.T) {
	const n = 20000

	seen := make(map[string]struct{}, n)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 并发生成，验证唯一性与并发安全
	workers := 8
	per := n / workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]string, 0, per)
			for i := 0; i < per; i++ {
				local = append(local, New())
			}
			mu.Lock()
			for _, s := range local {
				seen[s] = struct{}{}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != workers*per {
		t.Errorf("生成了 %d 个不同 ID，期望 %d（存在碰撞）", len(seen), workers*per)
	}
}

func TestMonotonicWithinSameMillisecond(t *testing.T) {
	// ulid.Monotonic 保证同毫秒内严格递增；用于按时间排序的稳定性
	prev := New()
	for i := 0; i < 1000; i++ {
		cur := New()
		if cur <= prev {
			t.Fatalf("ULID 未保持单调递增：%s 之后生成了 %s", prev, cur)
		}
		prev = cur
	}
}

func TestValid(t *testing.T) {
	if Valid("") {
		t.Error("空字符串不应合法")
	}
	if Valid("not-a-ulid") {
		t.Error("非法格式不应合法")
	}
	if Valid("01J8XQ0000000000000000000") { // 25 字符，长度不对
		t.Error("长度不足不应合法")
	}
	if !Valid(New()) {
		t.Error("生成的 ULID 应当合法")
	}
}
