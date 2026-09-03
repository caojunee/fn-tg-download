package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"tg-download/internal/settings"
	"tg-download/internal/tdl"
)

// Options 任务管理器依赖。
type Options struct {
	Settings   *settings.Store
	DataDir    string    // 任务持久化目录
	TdlDataDir string    // tdl bolt 存储目录
	Namespace  string    //
	TdlPath    string    //
	Persist    Persister // 任务持久化实现；nil 时回退到 JSON 文件
}

// Manager 管理任务：创建、调度、进度、持久化。
// 每个任务对应一个 tdl dl 子进程；暂停 = 终止进程（tdl --continue 可续传）。
type Manager struct {
	opts Options

	mu      sync.Mutex
	tasks   []*Task
	running map[string]*runner
	wg      sync.WaitGroup
}

type runner struct {
	dl      *tdl.RunningDownload
	stop    chan struct{}
	stopped chan struct{}
	base    int64   // 启动时目录字节数
	percent float64 // stdout 解析到的百分比（totalSize 未知时的兜底）
	speed   float64 // stdout 解析到的瞬时速率（字节/秒）
}

func NewManager(opts Options) *Manager {
	if opts.Persist == nil {
		opts.Persist = NewJSONPersister(opts.DataDir)
	}
	return &Manager{opts: opts, running: map[string]*runner{}}
}

// Load 从持久化层恢复任务列表；进行中/等待中的任务在服务重启后置为暂停。
func (m *Manager) Load() error {
	tasks, err := m.opts.Persist.Load()
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Status.Active() {
			t.Status = StatusPaused
			t.Speed = 0
			t.Error = "服务重启，任务已暂停，可手动继续"
		}
	}
	m.tasks = tasks
	return nil
}

func (m *Manager) persistLocked() {
	_ = m.opts.Persist.Save(m.tasks)
}

// List 返回任务快照。
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.clone())
	}
	return out
}

// CreateInput 创建任务的输入。
type CreateInput struct {
	URLs        []string
	Group       bool
	SavePath    string
	Include     []string
	Exclude     []string
	ProbeResult map[string]tdl.ProbeResult
}

// Create 为每个 URL 建任务并调度。
func (m *Manager) Create(in CreateInput) ([]Task, error) {
	if len(in.URLs) == 0 {
		return nil, errors.New("no urls")
	}
	cfg := m.opts.Settings.Get()
	savePath := strings.TrimSpace(in.SavePath)
	if savePath == "" {
		savePath = cfg.DownloadPath
	}
	if savePath == "" {
		return nil, errors.New("download path is empty, please set it in system settings first")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	created := make([]Task, 0, len(in.URLs))
	for _, raw := range in.URLs {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		// 同一 URL 已有活跃任务则跳过
		dup := false
		for _, t := range m.tasks {
			if t.URL == url && t.Status.Active() {
				dup = true
				break
			}
		}
		if dup {
			continue
		}

		t := &Task{
			ID:        newID(),
			URL:       url,
			Group:     in.Group,
			Status:    StatusPending,
			SavePath:  savePath,
			CreatedAt: time.Now().UnixMilli(),
		}
		if pr, ok := in.ProbeResult[url]; ok {
			t.ChatTitle = pr.ChatTitle
			t.MessageID = pr.MessageID
			t.Kind = pr.Kind
			t.TotalSize = pr.TotalSize
			t.Name = pr.Title
			t.Files = placeholderFiles(pr.FileCount, pr.TotalSize, pr.Title)
		}
		if t.Name == "" {
			t.Name = url
			t.Kind = "single"
			t.Files = placeholderFiles(1, 0, url)
		}
		if in.Group && t.Kind == "single" {
			t.Kind = "album"
		}
		m.tasks = append(m.tasks, t)
		created = append(created, t.clone())
	}

	m.persistLocked()
	m.pumpLocked()
	return created, nil
}

// placeholderFiles 根据 probe 结果生成文件条目（文件级进度为占位展示，
// 任务完成后会用目录中的真实文件回填）。
func placeholderFiles(count int, totalSize int64, title string) []File {
	if count <= 0 {
		count = 1
	}
	per := int64(0)
	if totalSize > 0 {
		per = totalSize / int64(count)
	}
	files := make([]File, 0, count)
	for i := 0; i < count; i++ {
		name := title
		if count > 1 {
			name = fmt.Sprintf("%s · 文件 %d", title, i+1)
		}
		files = append(files, File{ID: newID(), Name: name, Size: per, Status: StatusPending})
	}
	return files
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "t_" + hex.EncodeToString(b)
}

// Pause 暂停任务（终止子进程，tdl 会保存断点）。
func (m *Manager) Pause(id string) error {
	m.mu.Lock()
	t := m.findLocked(id)
	if t == nil {
		m.mu.Unlock()
		return errors.New("task not found")
	}
	if t.Status != StatusDownloading {
		m.mu.Unlock()
		return fmt.Errorf("task is %s, cannot pause", t.Status)
	}
	t.Status = StatusPaused
	t.Speed = 0
	r := m.running[id]
	delete(m.running, id)
	m.persistLocked()
	m.mu.Unlock()

	if r != nil {
		r.dl.Stop()
		select {
		case <-r.dl.Done:
		case <-time.After(6 * time.Second):
		}
		close(r.stop)
		<-r.stopped
	}
	return nil
}

// Resume 恢复任务。
func (m *Manager) Resume(id string) error {
	m.mu.Lock()
	t := m.findLocked(id)
	if t == nil {
		m.mu.Unlock()
		return errors.New("task not found")
	}
	if t.Status != StatusPaused && t.Status != StatusPending {
		m.mu.Unlock()
		return fmt.Errorf("task is %s, cannot resume", t.Status)
	}
	t.Status = StatusPending
	t.Error = ""
	m.persistLocked()
	m.pumpLocked()
	m.mu.Unlock()
	return nil
}

// Cancel 取消任务（终止进程并标记取消）。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	t := m.findLocked(id)
	if t == nil {
		m.mu.Unlock()
		return errors.New("task not found")
	}
	if !t.Status.Active() {
		m.mu.Unlock()
		return fmt.Errorf("task is %s, cannot cancel", t.Status)
	}
	t.Status = StatusCanceled
	t.FinishedAt = time.Now().UnixMilli()
	t.Speed = 0
	r := m.running[id]
	delete(m.running, id)
	m.persistLocked()
	m.mu.Unlock()

	if r != nil {
		r.dl.Stop()
		select {
		case <-r.dl.Done:
		case <-time.After(6 * time.Second):
		}
		close(r.stop)
		<-r.stopped
	}
	return nil
}

// Retry 重新入队失败/取消/已完成的任务。
func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.findLocked(id)
	if t == nil {
		return errors.New("task not found")
	}
	if t.Status.Active() {
		return fmt.Errorf("task is %s", t.Status)
	}
	t.Status = StatusPending
	t.DownloadedSize = 0
	t.Progress = 0
	t.Speed = 0
	t.Error = ""
	t.FinishedAt = 0
	m.persistLocked()
	m.pumpLocked()
	return nil
}

// Remove 删除历史记录（仅限非活跃任务）。
func (m *Manager) Remove(ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rm := map[string]bool{}
	for _, id := range ids {
		rm[id] = true
	}
	kept := m.tasks[:0]
	var removed []string
	for _, t := range m.tasks {
		if rm[t.ID] && !t.Status.Active() {
			removed = append(removed, t.ID)
			continue
		}
		kept = append(kept, t)
	}
	m.tasks = kept
	if err := m.opts.Persist.Delete(removed); err != nil {
		return err
	}
	return nil
}

// ClearHistory 清空非活跃任务记录。
func (m *Manager) ClearHistory() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.tasks[:0]
	for _, t := range m.tasks {
		if !t.Status.Active() {
			continue
		}
		kept = append(kept, t)
	}
	m.tasks = kept
	if err := m.opts.Persist.DeleteFinished(); err != nil {
		return err
	}
	return nil
}

func (m *Manager) findLocked(id string) *Task {
	for _, t := range m.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// pumpLocked 调度：把 pending 任务按并发限制拉起（需持有 m.mu）。
func (m *Manager) pumpLocked() {
	cfg := m.opts.Settings.Get()
	limit := cfg.Concurrency
	if limit <= 0 {
		limit = 4
	}
	for _, t := range m.tasks {
		if len(m.running) >= limit {
			return
		}
		if t.Status != StatusPending {
			continue
		}
		if _, ok := m.running[t.ID]; ok {
			continue
		}
		m.startLocked(t)
	}
}

// startLocked 启动一个任务（需持有 m.mu）。
func (m *Manager) startLocked(task *Task) {
	cfg := m.opts.Settings.Get()

	task.Status = StatusDownloading
	task.Error = ""

	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{stop: make(chan struct{}), stopped: make(chan struct{})}
	m.running[task.ID] = r

	_ = os.MkdirAll(task.SavePath, 0o755)
	r.base = dirSize(task.SavePath)

	opts := tdl.DownloadOptions{
		TdlPath:   m.opts.TdlPath,
		DataDir:   m.opts.TdlDataDir,
		Namespace: m.opts.Namespace,
		Proxy:     cfg.Proxy,
		URL:       task.URL,
		SavePath:  task.SavePath,
		Group:     task.Group,
		Limit:     2,
		Threads:   4,
		Include:   nil,
		Exclude:   nil,
	}

	dl, err := tdl.Start(ctx, opts, func(line string) {
		if s, ok := tdl.ParseProgressLine(line); ok {
			// 多文件并行时每个 tracker 各有一行，取最大百分比近似总进度
			if s.Percent > r.percent {
				r.percent = s.Percent
			}
			// 瞬时速率：读取方每秒清零，空闲时自然衰减
			if s.Speed > r.speed {
				r.speed = s.Speed
			}
		}
	})
	if err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
		task.FinishedAt = time.Now().UnixMilli()
		delete(m.running, task.ID)
		cancel()
		m.persistLocked()
		return
	}
	r.dl = dl

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.watchTask(task, r)
	}()
}

// watchTask 监控任务直到结束：目录大小差分为主，stdout 百分比为兜底。
// tdl 会预分配文件，目录差分可能长时间为 0 后突然跳变，
// 因此速率对 stdout 值与目录差分做加权平滑。
func (m *Manager) watchTask(task *Task, r *runner) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	last := r.base
	for {
		select {
		case <-tick.C:
			cur := dirSize(task.SavePath)
			delta := cur - last
			if delta < 0 {
				delta = 0
			}
			last = cur

			m.mu.Lock()
			if task.Status == StatusDownloading {
				stdoutSpeed := r.speed
				r.speed = 0
				// 平滑：优先目录差分，长期为 0 时采信 stdout 速率
				smoothed := 0.6*float64(task.Speed) + 0.4*float64(delta)
				if smoothed < 512 && stdoutSpeed > 1024 {
					smoothed = stdoutSpeed
				}
				task.Speed = int64(smoothed)
				// 已下载量单调递增：恢复任务时 tdl 可能重建 .tmp 文件，
				// 目录字节数短暂回缩会造成进度倒退
				if dl := cur - r.base; dl > task.DownloadedSize {
					task.DownloadedSize = dl
				}
				switch {
				case task.TotalSize > 0:
					p := float64(task.DownloadedSize) / float64(task.TotalSize) * 100
					if p > 100 {
						p = 100
					}
					if p < 0 {
						p = 0
					}
					task.Progress = p
				case r.percent > 0:
					if r.percent > task.Progress || task.Progress > 100 {
						task.Progress = r.percent
					}
				}
				m.persistLocked()
			}
			m.mu.Unlock()
		case exitErr, ok := <-r.dl.Done:
			if !ok {
				return
			}
			m.finishTask(task, r, exitErr)
			return
		case <-r.stop:
			// Pause/Cancel 已终止进程并更新状态，这里只等进程真正退出
			<-r.dl.Done
			close(r.stopped)
			return
		}
	}
}

func (m *Manager) finishTask(task *Task, r *runner, exitErr error) {
	m.mu.Lock()
	if task.Status == StatusDownloading {
		if exitErr != nil {
			task.Status = StatusFailed
			task.Error = trimErr(exitErr)
		} else {
			task.Status = StatusCompleted
			task.Progress = 100
			if task.TotalSize > 0 {
				task.DownloadedSize = task.TotalSize
			}
			fillFilesFromDir(task)
		}
		task.Speed = 0
		task.FinishedAt = time.Now().UnixMilli()
	}
	delete(m.running, task.ID)
	m.persistLocked()
	m.pumpLocked()
	m.mu.Unlock()
	close(r.stopped)
}

func trimErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// fillFilesFromDir 任务完成后用目录中的真实文件回填文件列表。
func fillFilesFromDir(t *Task) {
	entries, err := os.ReadDir(t.SavePath)
	if err != nil {
		return
	}
	files := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, File{
			ID: newID(), Name: e.Name(), Size: info.Size(),
			Downloaded: info.Size(), Progress: 100, Status: StatusCompleted,
		})
	}
	if len(files) > 0 {
		t.Files = files
	}
}

// dirSize 统计目录（单层）内文件总字节数。
func dirSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// Shutdown 停止所有运行中的任务（服务关闭时调用）。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	runners := make([]*runner, 0, len(m.running))
	for id, r := range m.running {
		runners = append(runners, r)
		delete(m.running, id)
	}
	for _, t := range m.tasks {
		if t.Status == StatusDownloading {
			t.Status = StatusPaused
			t.Speed = 0
		}
	}
	m.persistLocked()
	m.mu.Unlock()

	for _, r := range runners {
		r.dl.Stop()
		select {
		case <-r.dl.Done:
		case <-time.After(6 * time.Second):
		}
		close(r.stop)
		<-r.stopped
	}
}
