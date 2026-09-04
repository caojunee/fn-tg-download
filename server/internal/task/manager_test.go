package task

import (
	"fmt"
	"strings"
	"testing"
)

// 回归：暂停恢复后同一文件的 .tmp 名与最终名重复出现（2/5 问题）。
func TestSyncFilesLockedNoTmpDuplicates(t *testing.T) {
	task := &Task{
		Files: []File{
			{ID: "a", Name: "4434014100_2_video.mp4", Status: StatusCompleted, Progress: 100, Size: 100},
			{ID: "b", Name: "4434014100_3_video.mp4.tmp", Status: StatusPaused, Progress: 40, Size: 50},
			{ID: "c", Name: "4434014100_4_video.mp4.tmp", Status: StatusPaused, Progress: 0, Size: 0},
		},
	}
	// 恢复后 stdout 追踪到 3 个文件，名字已统一为最终名
	percs := map[string]float64{
		"4434014100_2_video.mp4": 100,
		"4434014100_3_video.mp4": 60,
		"4434014100_4_video.mp4": 0,
	}
	syncFilesLocked(task, percs, 3)

	if len(task.Files) != 3 {
		t.Fatalf("want 3 files, got %d: %v", len(task.Files), fileNames(task.Files))
	}
	seen := map[string]bool{}
	for _, f := range task.Files {
		if seen[f.Name] {
			t.Errorf("duplicate file entry: %s", f.Name)
		}
		seen[f.Name] = true
		if hasTmpSuffix(f.Name) {
			t.Errorf("entry keeps .tmp suffix: %s", f.Name)
		}
	}
	// 完成文件的 ID/Size 应从旧条目（曾带 .tmp 名）接续保留
	for _, f := range task.Files {
		if f.Name == "4434014100_2_video.mp4" && (f.ID != "a" || f.Size != 100) {
			t.Errorf("renamed file lost identity: id=%s size=%d", f.ID, f.Size)
		}
		if f.Name == "4434014100_3_video.mp4" && (f.ID != "b" || f.Size != 50) {
			t.Errorf("renamed file lost identity: id=%s size=%d", f.ID, f.Size)
		}
	}
}

// 回归：排队未开始的文件从列表消失（界面 0/2 而磁盘 3 个分片）。
func TestSyncFilesLockedQueuedPlaceholder(t *testing.T) {
	task := &Task{
		ID:   "t_x",
		Name: "test",
		// 探测占位 3 条（名字不匹配 fileRe）
		Files: []File{
			{ID: "p1", Name: "test · 文件 1", Status: StatusPending},
			{ID: "p2", Name: "test · 文件 2", Status: StatusPending},
			{ID: "p3", Name: "test · 文件 3", Status: StatusPending},
		},
	}
	// tracker 只覆盖正在下载的 2 个
	percs := map[string]float64{
		"4434014100_2_video.mp4": 30,
		"4434014100_3_video.mp4": 10,
	}
	syncFilesLocked(task, percs, 3)

	if len(task.Files) != 3 {
		t.Fatalf("want 3 files (2 downloading + 1 queued), got %d: %v", len(task.Files), fileNames(task.Files))
	}
	queued := 0
	for _, f := range task.Files {
		if strings.Contains(f.Name, "排队") {
			queued++
			if f.Status != StatusPending {
				t.Errorf("queued entry status = %s, want pending", f.Status)
			}
		}
	}
	if queued != 1 {
		t.Fatalf("want 1 queued placeholder, got %d", queued)
	}

	// 跨 tick 稳定：第二次同步占位不重复、ID 不变
	first := make(map[string]string)
	for _, f := range task.Files {
		first[f.Name] = f.ID
	}
	syncFilesLocked(task, percs, 3)
	if len(task.Files) != 3 {
		t.Fatalf("tick2: want 3 files, got %d", len(task.Files))
	}
	for _, f := range task.Files {
		if first[f.Name] != f.ID {
			t.Errorf("tick2: entry %s changed id %s -> %s", f.Name, first[f.Name], f.ID)
		}
	}
}

func fileNames(fs []File) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

var _ = fmt.Sprintf
func hasTmpSuffix(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".tmp"
}
