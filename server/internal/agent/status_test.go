package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func testStatusLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPatchStatusComplete 校验补丁齐备的判定。
func TestPatchStatusComplete(t *testing.T) {
	cases := []struct {
		name  string
		patch PatchStatus
		want  bool
	}{
		{"两个补丁都在", PatchStatus{UploaderTarget: true, ContextData: true}, true},
		{"缺 uploader", PatchStatus{UploaderTarget: false, ContextData: true}, false},
		{"缺 contextData", PatchStatus{UploaderTarget: true, ContextData: false}, false},
		{"都缺", PatchStatus{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.patch.Complete(); got != tc.want {
				t.Errorf("Complete() = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestPatchStatusDescribe 校验缺失说明包含关键信息（便于用户自助排查）。
func TestPatchStatusDescribe(t *testing.T) {
	full := PatchStatus{UploaderTarget: true, ContextData: true}
	if got := full.Describe(); got != "" {
		t.Errorf("补丁齐备时应返回空串，实际 %q", got)
	}

	partial := PatchStatus{
		UploaderTarget: true,
		ContextData:    false,
		PackageName:    "picgo",
		PackageVersion: "3.0.2",
		Error:          "缺补丁",
	}
	got := partial.Describe()
	// 必须说清「缺什么」与「当前是什么包」——这两点决定了用户下一步怎么办
	for _, want := range []string{"UploadOptions.contextData", "picgo@3.0.2", "缺补丁"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() 应包含 %q，实际 %q", want, got)
		}
	}
	// uploaderTarget 已就位 → 不应出现在缺失列表里
	if strings.Contains(got, "UploadOptions.uploader,") ||
		strings.Contains(got, "UploadOptions.uploader）") {
		t.Errorf("Describe() 不应把已就位的补丁列为缺失: %q", got)
	}
}

// TestStatusHolderPatchesComplete 校验「agent 不可用时保守返回 false」。
//
// 为什么必须保守：agent 掉线时无法确认补丁状态，
// 若乐观返回 true，并发会退回「靠全局 picBed.uploader」的不安全路径。
func TestStatusHolderPatchesComplete(t *testing.T) {
	h := NewStatusHolder()

	// 初始（未探测）→ 保守
	if h.PatchesComplete() {
		t.Error("未探测时应保守返回 false（agent 未确认补丁）")
	}

	// agent 就绪 + 补丁齐备
	h.SetUp(&HealthzData{
		PicgoVersion: "3.0.2",
		Patches:      PatchStatus{UploaderTarget: true, ContextData: true},
	})
	if !h.PatchesComplete() {
		t.Error("agent 可用且补丁齐备时应返回 true")
	}

	// agent 掉线 → 仍保守（即使之前探测到补丁齐备）
	h.SetDown(errors.New("连接被拒绝"))
	if h.PatchesComplete() {
		t.Error("agent 不可用时应保守返回 false（无法确认补丁状态）")
	}

	// 掉线不应丢失已探测到的补丁信息（便于日志说明）
	if !h.Get().Patches.Complete() {
		t.Error("掉线时不应清空已探测到的补丁状态（仅 Up 变 false）")
	}

	// 补丁不全
	h2 := NewStatusHolder()
	h2.SetUp(&HealthzData{Patches: PatchStatus{UploaderTarget: true, ContextData: false}})
	if h2.PatchesComplete() {
		t.Error("补丁不全时应返回 false")
	}
}

// TestStatusHolderRefreshRecordsPatches 校验 Refresh 会把补丁状态缓存下来。
func TestStatusHolderRefreshRecordsPatches(t *testing.T) {
	// /healthz 不使用信封（直接返回扁平对象）
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HealthzData{
			Ok:           true,
			PicgoVersion: "1.2.3",
			Patches: PatchStatus{
				UploaderTarget: true,
				ContextData:    true,
				PackageName:    "@yeqingky/picgo-core",
				PackageVersion: "1.0.0",
			},
		})
	})

	h := NewStatusHolder()
	snap := h.Refresh(context.Background(), client)

	if !snap.Up {
		t.Fatalf("应探测为可用，err=%s", snap.Error)
	}
	if !snap.Patches.Complete() {
		t.Error("Refresh 后应记录到补丁齐备")
	}
	if snap.Patches.PackageName != "@yeqingky/picgo-core" {
		t.Errorf("应记录包名，实际 %q", snap.Patches.PackageName)
	}
	if !h.PatchesComplete() {
		t.Error("PatchesComplete() 应反映刷新后的状态")
	}
}

// TestStatusHolderNilSafe 校验 nil 接收者不会 panic（handler 里 Deps 可能为 nil）。
func TestStatusHolderNilSafe(t *testing.T) {
	var h *StatusHolder
	if got := h.Get(); got.Up {
		t.Error("nil holder 的 Get 应返回零值")
	}
	if h.PatchesComplete() {
		t.Error("nil holder 的 PatchesComplete 应返回 false")
	}
	h.SetUp(&HealthzData{Patches: PatchStatus{UploaderTarget: true, ContextData: true}})
	h.SetDown(nil)
}
