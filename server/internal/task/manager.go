package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	fm          sync.Mutex         // 保护 filePerc/fileNames（onLine 与 watchTask 不同 goroutine）
	filePerc    map[string]float64 // stdout 追踪到的下载单元 → 最新百分比，键为 "对话ID_消息ID"
	fileNames   map[string]string  // 已解析到的真实磁盘文件名，键同上（可能滞后一拍）
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

// fileRe 匹配 tdl 默认文件名模板 {对话ID}_{消息ID}_{文件名}，
// 用于从进度行中排除 CPU/Memory 等系统统计行。
var fileRe = regexp.MustCompile(`^\d+_\d+_.+`)

// syncFilesLocked 用 stdout 追踪到的真实文件名重建任务的文件列表。
// 前提：持有 m.mu 与 runner.fm。每次全量重建，可自愈清理此前误入的
// 统计行条目；私有频道探测失败导致的占位列表也会被真实数据替换。
func syncFilesLocked(t *Task, percs map[string]float64) {
	names := make([]string, 0, len(percs))
	for n := range percs {
		if fileRe.MatchString(n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)

	old := make(map[string]File, len(t.Files))
	for _, f := range t.Files {
		old[f.Name] = f
	}
	files := make([]File, 0, len(names))
	for _, n := range names {
		id := newID()
		if of, ok := old[n]; ok {
			id = of.ID // 保持 ID 稳定，避免前端列表抖动
		}
		p := percs[n]
		st := StatusDownloading
		if p >= 100 {
			st = StatusCompleted
		}
		files = append(files, File{ID: id, Name: n, Status: st, Progress: p})
	}
	t.Files = files
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
// deletePartial 为 true 时，删除该任务已下载的 .tmp 分片（放弃断点续传）。
func (m *Manager) Cancel(id string, deletePartial bool) error {
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
		if deletePartial {
			r.fm.Lock()
			keys := make([]string, 0, len(r.filePerc))
			for k := range r.filePerc {
				keys = append(keys, k)
			}
			r.fm.Unlock()
			removePartials(t.SavePath, keys, m.otherRunningPrefixes(id))
		}
	}
	return nil
}

// otherRunningPrefixes 收集除 exclude 外所有运行中任务追踪到的下载单元前缀
// （"对话ID_消息ID"），删除分片时用于保护其他并发任务的断点数据。
func (m *Manager) otherRunningPrefixes(exclude string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	prot := make(map[string]bool)
	for id, r := range m.running {
		if id == exclude {
			continue
		}
		r.fm.Lock()
		for k := range r.filePerc {
			prot[k] = true
		}
		r.fm.Unlock()
	}
	return prot
}

// removePartials 只删除 .tmp 分片，绝不碰已完成的真实文件。
// 删除规则：文件名前缀（"对话ID_消息ID"）属于本任务追踪到的 keys，或
// 不属于任何其他运行中任务追踪到的前缀（保护并发任务的断点）。
// 后一条件覆盖排队未启动的分片——它们从未出现在 tracker 输出里，
// 但 0 字节/未开始的分片删除无实际损失（重试会重建）。
func removePartials(dir string, keys []string, protected map[string]bool) {
	if dir == "" {
		return
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		if k != "" {
			keySet[k] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".tmp") {
			continue
		}
		prefix := partialPrefix(strings.TrimSuffix(name, ".tmp"))
		if prefix == "" {
			continue
		}
		if keySet[prefix] || !protected[prefix] {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// partialPrefix 从分片文件名（去 .tmp 后）提取 "对话ID_消息ID" 前缀，
// 即 tdl 默认模板 {对话ID}_{消息ID}_{文件名} 的前两段；不匹配返回空。
var partialPrefixRe = regexp.MustCompile(`^(\d+)_(\d+)_`)

func partialPrefix(base string) string {
	m := partialPrefixRe.FindString(base)
	return strings.TrimSuffix(m, "_")
}

// resolveFileNames 把 stdout 追踪到的下载单元键（"对话ID_消息ID"）解析为
// 磁盘上的真实文件名（{对话ID}_{消息ID}_{文件名}，完成后无 .tmp 后缀）。
// 文件尚未创建或已被 tdl 重命名时重新解析。需持有 r.fm。
func resolveFileNames(savePath string, r *runner) map[string]string {
	if r.fileNames == nil {
		r.fileNames = make(map[string]string)
	}
	for key := range r.filePerc {
		if n, ok := r.fileNames[key]; ok && n != "" {
			if _, err := os.Stat(filepath.Join(savePath, n)); err == nil {
				continue // 已解析且文件仍在
			}
		}
		matches, err := filepath.Glob(filepath.Join(savePath, key+"_*"))
		if err != nil || len(matches) == 0 {
			continue // 文件还没落盘，下一秒 tick 重试
		}
		sort.Strings(matches)
		r.fileNames[key] = filepath.Base(matches[0])
	}
	return r.fileNames
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
			// 追踪下载单元（键 "对话ID_消息ID"）。tracker 行不含文件名，
			// 真实文件名由 watchTask 按 {对话ID}_{消息ID}_ 前缀从磁盘解析
			if s.DialogID != "" && s.MsgID != "" {
				r.fm.Lock()
				if r.filePerc == nil {
					r.filePerc = make(map[string]float64)
				}
				r.filePerc[s.DialogID+"_"+s.MsgID] = s.Percent
				r.fm.Unlock()
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
				// 用 stdout 追踪到的真实文件列表修正占位数据
				r.fm.Lock()
				if len(r.filePerc) > 0 {
					percs := make(map[string]float64, len(r.filePerc))
					for key, p := range r.filePerc {
						if n, ok := resolveFileNames(task.SavePath, r)[key]; ok && n != "" {
							percs[n] = p
						}
					}
					if len(percs) > 0 {
						syncFilesLocked(task, percs)
					}
				}
				r.fm.Unlock()
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
			fillFilesFinal(task, r)
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

// fillFilesFinal 任务完成后的文件回填：优先使用 stdout 追踪到的本任务
// 文件名（避免同目录其他任务的文件被并入），拿不到时退回目录扫描。
func fillFilesFinal(t *Task, r *runner) {
	r.fm.Lock()
	percs := make(map[string]float64, len(r.filePerc))
	for n, p := range r.filePerc {
		if fileRe.MatchString(n) {
			percs[n] = p
		}
	}
	r.fm.Unlock()

	if len(percs) > 0 {
		names := make([]string, 0, len(percs))
		for n := range percs {
			names = append(names, n)
		}
		sort.Strings(names)
		files := make([]File, 0, len(names))
		for _, n := range names {
			size := fileSize(t.SavePath, n)
			files = append(files, File{
				ID: newID(), Name: n, Size: size,
				Downloaded: size, Progress: 100, Status: StatusCompleted,
			})
		}
		t.Files = files
		return
	}
	fillFilesFromDir(t)
}

// fileSize 在目录中查找文件大小（兼容 tdl 完成时已去掉 .tmp 后缀的情况）。
func fileSize(dir, name string) int64 {
	for _, cand := range []string{name, name + ".tmp"} {
		if info, err := os.Stat(filepath.Join(dir, cand)); err == nil {
			return info.Size()
		}
	}
	return 0
}

// fillFilesFromDir 任务完成后用目录中的真实文件回填文件列表（跳过 .tmp 分片）。
func fillFilesFromDir(t *Task) {
	entries, err := os.ReadDir(t.SavePath)
	if err != nil {
		return
	}
	files := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
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
