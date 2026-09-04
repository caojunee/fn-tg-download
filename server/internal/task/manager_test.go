package task

import "testing"

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
	syncFilesLocked(task, percs)

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

func fileNames(fs []File) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func hasTmpSuffix(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".tmp"
}
