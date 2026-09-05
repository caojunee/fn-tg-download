package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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
	export  *tdl.RunningExport // 整频道任务：导出消息列表阶段（dl 启动前置步骤）
	stop    chan struct{}
	stopped chan struct{}
	base    int64   // 启动时目录字节数
	percent float64 // stdout 解析到的百分比（totalSize 未知时的兜底）
	speed   float64 // stdout 解析到的瞬时速率（字节/秒）

	expectedFiles int // 探测/导出阶段已知的期望文件数，首次同步时捕获

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

// Selection 一条链接上用户勾选的下载范围。
type Selection struct {
	// PeerID 为 tdl 语义的会话 ID，用于生成 tdl -f 清单
	PeerID int64
	// MsgIDs 勾选的消息 ID，为空视为全量
	MsgIDs []int
	// Files 为可选项：来自资源清单缓存的名称 / 大小，用于预填任务文件列表
	Files []File
}

// CreateInput 创建任务的输入。
type CreateInput struct {
	URLs        []string
	Group       bool
	SavePath    string
	Include     []string
	Exclude     []string
	ProbeResult map[string]tdl.ProbeResult
	// Selection 以 URL 为键的选择性下载范围；未包含的 URL 走全量下载
	Selection map[string]Selection
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
	// 选择性下载：记录会话 ID 与勾选的消息，文件列表用清单缓存的实际
	// 名称 / 大小预填（缓存未命中时回退为占位条目）
	if sel, ok := in.Selection[url]; ok && len(sel.MsgIDs) > 0 {
		t.PeerID = sel.PeerID
		t.Selected = sel.MsgIDs
		total := int64(0)
		files := make([]File, 0, len(sel.MsgIDs))
		for i, id := range sel.MsgIDs {
			if i < len(sel.Files) {
				files = append(files, sel.Files[i])
				total += sel.Files[i].Size
				continue
			}
			files = append(files, File{
				ID:   fmt.Sprintf("%d_%d", sel.PeerID, id),
				Name: fmt.Sprintf("%d_%d", sel.PeerID, id),
			})
		}
		t.Files = files
		t.TotalSize = total
		if t.Kind != "channel" {
			t.Kind = "selection"
		}
	}
	// 按频道名自动分子目录（与系统设置页文案一致）：
	// <保存路径>/<频道名>。标题缺失时由 DirNameForURL 从链接回退
	// （私有频道「频道 <id>」/ 公开频道 username），无法解析则不分子目录。
	if sub := tdl.DirNameForURL(url, t.ChatTitle); sub != "" {
		t.SavePath = filepath.Join(savePath, sub)
	}
	if t.Name == "" {
		t.Name = url
		t.Kind = "single"
		t.Files = placeholderFiles(1, 0, url)
	}
	if t.Kind == "channel" && len(t.Selected) == 0 {
		// 整频道任务：真实文件数/大小在启动后的导出阶段填充
		t.Files = placeholderFiles(1, 0, t.Name)
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
// 前提：持有 m.mu 与 runner.fm。追踪条目全量重建（自愈清理此前误入的
// 统计行条目）；旧列表中形如真实文件的条目（整频道任务里排队未启动的）
// 保留为 pending，不丢文件。旧条目的真实大小按名字匹配保留
// （下载中的名字带 .tmp 后缀，先去掉再比对）。
// expected 为探测/导出阶段已知的期望文件数：tracker 只覆盖正在下载的
// 单元，排队未开始的文件会在此补回占位条目，保持列表与磁盘一致。
func syncFilesLocked(t *Task, percs map[string]float64, expected int) {
	names := make([]string, 0, len(percs))
	for n := range percs {
		if fileRe.MatchString(n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
	}

	old := make(map[string]File, len(t.Files))
	for _, f := range t.Files {
		// 键统一用最终名：旧条目可能是改名前带 .tmp 的名字
		old[strings.TrimSuffix(f.Name, ".tmp")] = f
	}

	files := make([]File, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		id := newID()
		size := int64(0)
		if of, ok := old[n]; ok {
			id = of.ID // 保持 ID 稳定，避免前端列表抖动
			size = of.Size
		}
		// 磁盘字节数优先：占位期均分的估算 size（或坏数据）以磁盘为准；
		// 导出得到的真实总大小大于已下载字节时保留原值
		if ds := fileSize(t.SavePath, n); ds > size {
			size = ds
		}
		p := percs[n]
		st := StatusDownloading
		if p >= 100 {
			st = StatusCompleted
		}
		files = append(files, File{ID: id, Name: n, Size: size, Status: st, Progress: p})
		seen[n] = true
	}
	// 旧列表里未被追踪的真实文件条目（如整频道任务排队中的）保留。
	// 名字统一去 .tmp 后缀：tdl 完成时会改名，旧条目名可能还带 .tmp，
	// 与新列表的最终名是同一文件，不去重会重复出现（暂停恢复后 2/5 的根因）
	for _, f := range t.Files {
		name := strings.TrimSuffix(f.Name, ".tmp")
		if seen[name] || !fileRe.MatchString(name) {
			continue
		}
		st := f.Status
		if st == StatusDownloading {
			st = StatusPaused
		}
		size := f.Size
		if ds := fileSize(t.SavePath, name); ds > size {
			size = ds
		}
		files = append(files, File{ID: f.ID, Name: name, Size: size, Status: st, Progress: f.Progress})
		seen[name] = true
	}
	// 排队未开始的文件补占位：tracker 行只覆盖正在下载的单元，
	// 不补的话探测已知的文件会从列表消失（界面 0/2 而磁盘 3 个分片）
	for i := len(files); i < expected; i++ {
		files = append(files, File{
			ID:      fmt.Sprintf("q_%s_%d", t.ID, i),
			Name:    fmt.Sprintf("%s · 排队 %d", t.Name, i+1),
			Status:  StatusPending,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
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
		r.stopProc()
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
		r.stopProc()
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
		// tdl 下载中文件名带 .tmp 后缀，完成后自动改名；列表全程
		// 统一用最终名（去掉 .tmp），避免改名前后同一文件出现两条
		r.fileNames[key] = strings.TrimSuffix(filepath.Base(matches[0]), ".tmp")
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
	for _, id := range removed {
		_ = os.Remove(m.exportPath(id))
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
	// 清理所有已不在列表中的整频道导出文件
	entries, _ := os.ReadDir(filepath.Join(m.opts.DataDir, "exports"))
	alive := make(map[string]bool, len(m.tasks))
	for _, t := range m.tasks {
		alive[t.ID] = true
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if !alive[id] {
			_ = os.Remove(filepath.Join(m.opts.DataDir, "exports", e.Name()))
		}
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
		// tdl 断点续传为「文件级」：已完成的文件续传时秒跳过，但下载到一半的
		// 文件会被截断重下。单并发（-l 1）保证暂停/失败时最多只损失 1 个
		// 文件的部分进度。
		Limit:   1,
		Threads: 4,
		Include: nil,
		Exclude: nil,
	}

	// tdl 子进程的非进度输出（错误/panic 堆栈/警告）落服务日志：
	// 之前这些行被直接丢弃，任务失败只显示 "exit status N"，无法排错
	onLog := func(line string) {
		log.Printf("tdl[%s] %s", task.ID, line)
	}
	onLine := func(line string) {
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
	}

	// 选择性下载：按勾选的消息生成 tdl -f 清单直接下载，
	// 既跳过整频道导出阶段，也精确到「只下选中的这几条」
	if len(task.Selected) > 0 {
		selPath := m.selectionPath(task.ID)
		if err := tdl.WriteSelectionJSON(selPath, task.PeerID, task.Selected); err != nil {
			log.Printf("task %s build selection failed: %v", task.ID, err)
			cancel()
			m.failTask(task, r, "生成下载清单失败: "+err.Error())
			return
		}
		opts.File = selPath
		opts.Group = false // 清单已精确到消息，无需 tdl 再分组
		log.Printf("task %s start: selection peer=%d messages=%d file=%s save=%s tdl %s",
			task.ID, task.PeerID, len(task.Selected), selPath, opts.SavePath,
			strings.Join(tdl.BuildArgs(opts), " "))
		dl, err := tdl.Start(ctx, opts, onLine, onLog)
		if err != nil {
			log.Printf("task %s start failed: %v", task.ID, err)
			cancel()
			m.failTask(task, r, err.Error())
			return
		}
		r.setDL(dl)
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer cancel()
			m.watchTask(task, r)
		}()
		return
	}

	if task.Kind == "channel" {
		// 整频道任务：先导出频道媒体消息列表，再以 -f 模式批量下载
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer cancel()
			exportPath := m.exportPath(task.ID)
			if !m.runChannelExport(task, r, ctx, tdl.ExportOptions{
				TdlPath:   m.opts.TdlPath,
				DataDir:   m.opts.TdlDataDir,
				Namespace: m.opts.Namespace,
				Proxy:     cfg.Proxy,
				Chat:      m.chatFromURL(task.URL),
				Output:    exportPath,
			}) {
				return // 已终止或失败
			}
			opts.File = exportPath
			opts.Group = false // 导出列表已含全部相册消息，无需 tdl 再分组
			log.Printf("task %s start: save=%s tdl %s", task.ID, task.SavePath,
				strings.Join(tdl.BuildArgs(opts), " "))
			dl, err := tdl.Start(ctx, opts, onLine, onLog)
			if err != nil {
				log.Printf("task %s start failed: %v", task.ID, err)
				m.failTask(task, r, err.Error())
				return
			}
			r.setDL(dl)
			m.watchTask(task, r)
		}()
		return
	}

	log.Printf("task %s start: save=%s tdl %s", task.ID, task.SavePath,
		strings.Join(tdl.BuildArgs(opts), " "))
	dl, err := tdl.Start(ctx, opts, onLine, onLog)
	if err != nil {
		log.Printf("task %s start failed: %v", task.ID, err)
		task.Status = StatusFailed
		task.Error = err.Error()
		task.FinishedAt = time.Now().UnixMilli()
		delete(m.running, task.ID)
		cancel()
		m.persistLocked()
		return
	}
	r.setDL(dl)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.watchTask(task, r)
	}()
}

// selectionPath 选择性下载清单的路径（与整频道导出同目录，便于统一清理）。
func (m *Manager) selectionPath(taskID string) string {
	return filepath.Join(m.opts.DataDir, "exports", taskID+".selection.json")
}

// exportPath 整频道任务的消息导出文件路径。
func (m *Manager) exportPath(taskID string) string {
	return filepath.Join(m.opts.DataDir, "exports", taskID+".json")
}

// chatFromURL 从整频道链接提取 chat 标识（数字 ID 或用户名）。
// 解析失败返回原链接（交由 tdl 报错）。
func (m *Manager) chatFromURL(raw string) string {
	if chat, _, ok := tdl.ChannelLink(raw); ok {
		return chat
	}
	return raw
}

// setDL / setExport / getProc 带锁维护 runner 的子进程引用，
// 供 Pause/Cancel 与任务 goroutine 安全交接。
func (r *runner) setDL(dl *tdl.RunningDownload) {
	r.fm.Lock()
	r.dl = dl
	r.fm.Unlock()
}

func (r *runner) setExport(ex *tdl.RunningExport) {
	r.fm.Lock()
	r.export = ex
	r.fm.Unlock()
}

// stopProc 终止 runner 当前子进程（下载或导出）并等待其退出。
func (r *runner) stopProc() {
	r.fm.Lock()
	dl, ex := r.dl, r.export
	r.fm.Unlock()
	switch {
	case dl != nil:
		dl.Stop()
		select {
		case <-dl.Done:
		case <-time.After(6 * time.Second):
		}
	case ex != nil:
		ex.Stop()
		select {
		case <-ex.Done:
		case <-time.After(6 * time.Second):
		}
	}
}

// runChannelExport 整频道任务第一步：导出频道媒体消息列表并回填文件信息。
// 返回 false 表示任务已终止或失败。导出文件已存在时直接复用（暂停恢复/重试）。
func (m *Manager) runChannelExport(task *Task, r *runner, ctx context.Context, opts tdl.ExportOptions) bool {
	path := opts.Output
	if _, err := os.Stat(path); err != nil {
		log.Printf("task %s export: chat=%s out=%s", task.ID, opts.Chat, opts.Output)
		ex, err := tdl.StartExport(ctx, opts)
		if err != nil {
			m.failTask(task, r, "导出频道消息失败: "+err.Error())
			return false
		}
		r.setExport(ex)
		select {
		case err := <-ex.Done:
			r.setExport(nil)
			if err != nil {
				m.failTask(task, r, "导出频道消息失败: "+trimErr(err))
				return false
			}
		case <-r.stop:
			ex.Stop()
			<-ex.Done
			r.setExport(nil)
			close(r.stopped)
			return false
		}
	}

	files, err := tdl.ParseExportFile(path)
	if err != nil {
		_ = os.Remove(path) // 损坏的导出文件不留着，重试时会重新导出
		m.failTask(task, r, "解析频道导出文件失败: "+err.Error())
		return false
	}

	// 把真实文件列表与总大小写回任务（前端轮询即可见）
	total := int64(0)
	for _, f := range files {
		total += f.Size
	}
	m.mu.Lock()
	if task.Status == StatusDownloading {
		task.TotalSize = total
		fl := make([]File, 0, len(files))
		for _, f := range files {
			fl = append(fl, File{ID: newID(), Name: f.Name, Size: f.Size, Status: StatusPending})
		}
		task.Files = fl
		m.persistLocked()
	}
	m.mu.Unlock()
	return true
}

// failTask 标记任务失败并清理调度状态。
func (m *Manager) failTask(task *Task, r *runner, msg string) {
	log.Printf("task %s failed: %s", task.ID, msg)
	m.mu.Lock()
	if task.Status.Active() {
		task.Status = StatusFailed
		task.Error = msg
		task.Speed = 0
		task.FinishedAt = time.Now().UnixMilli()
	}
	delete(m.running, task.ID)
	m.persistLocked()
	m.pumpLocked()
	m.mu.Unlock()
	close(r.stopped)
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
				// 期望文件数：首次同步前捕获（此刻列表还是探测/导出的完整
				// 列表），并提升到磁盘分片前缀数（历史任务列表可能已丢条目，
				// 而 tdl 预创建全部分片，磁盘 key 数即真实文件数下限）
				if r.expectedFiles == 0 {
					r.expectedFiles = len(task.Files)
				}
				expected := r.expectedFiles
				if k := diskKeyCount(task.SavePath); k > expected {
					expected = k
					r.expectedFiles = k
				}
				if len(r.filePerc) > 0 {
					percs := make(map[string]float64, len(r.filePerc))
					for key, p := range r.filePerc {
						if n, ok := resolveFileNames(task.SavePath, r)[key]; ok && n != "" {
							percs[n] = p
						}
					}
					if len(percs) > 0 {
						syncFilesLocked(task, percs, expected)
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
				// stdout 百分比是"最大 tracker 百分比"，多文件任务中首个
				// 单元完成即为 100%，但其余仍在下载——封顶 99 避免误导，
				// 完成时由 finishTask 置 100
				if r.percent > task.Progress || task.Progress > 100 {
					p := r.percent
					if p > 99 {
						p = 99
					}
					if task.Progress < p {
						task.Progress = p
					}
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
			msg := trimErr(exitErr)
			// 附加 tdl 最后的非进度输出（真实错误/panic 堆栈尾行），
			// 否则前端只能看到 "exit status 2" 这种无信息量的退出码
			if r.dl != nil {
				if tail := r.dl.LogTail(); tail != "" {
					msg += "（tdl: " + tail + "）"
				}
			}
			task.Error = trimErr(errors.New(msg))
			log.Printf("task %s failed: %s", task.ID, task.Error)
		} else {
			task.Status = StatusCompleted
			task.Progress = 100
			if task.TotalSize > 0 {
				task.DownloadedSize = task.TotalSize
			}
			fillFilesFinal(task, r)
			// 文件大小合计回填已下载量：秒完成/重试的任务 watchTask 来不及
			// 累计目录差分，避免完成后显示 "0 B / 0 B"
			var sum int64
			for _, f := range task.Files {
				sum += f.Size
			}
			if sum > task.DownloadedSize {
				task.DownloadedSize = sum
			}
			if task.TotalSize < sum {
				task.TotalSize = sum
			}
			if len(task.Files) == 0 {
				// 空跑兜底：tdl 正常退出但没下载到任何文件（纯文本消息、
				// 全部被过滤等），标记失败并说明原因，避免误导性的
				// "completed 0 文件"
				task.Status = StatusFailed
				task.Error = "未下载到任何文件（消息可能不包含媒体或已失效）"
				task.Progress = 0
				log.Printf("task %s failed: no files downloaded", task.ID)
			} else {
				log.Printf("task %s completed: %d files, %d bytes", task.ID, len(task.Files), sum)
			}
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
	for key, p := range r.filePerc {
		if n, ok := resolveFileNames(t.SavePath, r)[key]; ok && n != "" {
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
		// 恢复任务时 tdl 会跳过之前已完成、大小一致的文件（无 tracker
		// 行），percs 只含本次运行的文件；把旧列表中未被覆盖的真实文件
		// 条目并入，避免完成后列表只剩本次下载的部分
		seen := make(map[string]bool, len(files))
		for _, f := range files {
			seen[f.Name] = true
		}
		for _, f := range t.Files {
			name := strings.TrimSuffix(f.Name, ".tmp")
			if seen[name] || !fileRe.MatchString(name) {
				continue
			}
			size := f.Size
			if size == 0 {
				size = fileSize(t.SavePath, name)
			}
			files = append(files, File{
				ID: f.ID, Name: name, Size: size,
				Downloaded: size, Progress: 100, Status: StatusCompleted,
			})
			seen[name] = true
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
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

// diskKeyRe 从磁盘文件名提取下载单元前缀（"对话ID_消息ID"）。
// tdl 按模板 {对话ID}_{消息ID}_{文件名}[.tmp] 落盘，同一单元一个文件。
var diskKeyRe = regexp.MustCompile(`^(\d+_\d+)_`)

// diskKeyCount 统计目录中出现的下载单元数。tdl 会为全部待下载文件
// 预创建分片，即使尚未开始下载，因此该数是任务真实文件数的下限，
// 用于修复列表丢条目后 expected 无法恢复的问题。
func diskKeyCount(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if m := diskKeyRe.FindStringSubmatch(e.Name()); m != nil {
			seen[m[1]] = true
		}
	}
	return len(seen)
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
