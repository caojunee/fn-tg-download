package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	Aria2Path  string    // aria2c 可执行文件（serve+aria2 字节级续传下载路径）
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

	// serveGate 串行化 serve 下载任务（tdl serve 独占 bolt 存储锁）
	serveGate chan struct{}
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

	// serve+aria2 下载路径（字节级续传）：
	serve      *tdl.ServeProc    // tdl serve 子进程（任务结束/暂停时终止）
	serveFiles []tdl.ServeFile   // serve 端点文件清单（取消清理分片用）
	ariaPct    float64           // 当前文件的 aria2 百分比
	ariaLastDl int64             // 上一秒累计已下载字节（速度差分用）

	fm          sync.Mutex         // 保护 filePerc/fileNames（onLine 与 watchTask 不同 goroutine）
	filePerc    map[string]float64 // stdout 追踪到的下载单元 → 最新百分比，键为 "对话ID_消息ID"
	fileNames   map[string]string  // 已解析到的真实磁盘文件名，键同上（可能滞后一拍）
}

func NewManager(opts Options) *Manager {
	if opts.Persist == nil {
		opts.Persist = NewJSONPersister(opts.DataDir)
	}
	// tdl serve 持有 bolt 存储锁：任意时刻只能有一个 serve 下载任务，
	// 其余任务在此信号量上排队（并发数大于 1 时也强制串行下载）
	return &Manager{opts: opts, running: map[string]*runner{}, serveGate: make(chan struct{}, 1)}
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
	// 暂停释放并发槽后，拉起排队中的 pending 任务
	m.mu.Lock()
	m.pumpLocked()
	m.mu.Unlock()
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
			serveFiles := r.serveFiles
			r.fm.Unlock()
			removePartials(t.SavePath, keys, m.otherRunningPrefixes(id))
			// serve 路径：删除半成品与 .aria2 控制文件
			if len(serveFiles) > 0 {
				removeServePartials(t.SavePath, serveFiles)
			}
		}
	}
	// 取消释放并发槽后，拉起排队中的 pending 任务
	m.mu.Lock()
	m.pumpLocked()
	m.mu.Unlock()
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
// 统一走 serve+aria2 路径：tdl dl --serve 把文件暴露成本地 HTTP 端点
// （支持 Range/206），aria2c -c 逐文件下载——暂停/失败后凭 .aria2 控制
// 文件实现字节级断点续传（tdl 内置下载器只有文件级续传，半成品 .tmp
// 会被截断重下）。
func (m *Manager) startLocked(task *Task) {
	cfg := m.opts.Settings.Get()

	task.Status = StatusDownloading
	task.Error = ""

	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{stop: make(chan struct{}), stopped: make(chan struct{})}
	m.running[task.ID] = r

	_ = os.MkdirAll(task.SavePath, 0o755)
	r.base = dirSize(task.SavePath)

	// 下载来源：selection 清单 / 链接（整频道在 goroutine 内先导出）
	srcURL, srcFile := task.URL, ""
	if len(task.Selected) > 0 {
		selPath := m.selectionPath(task.ID)
		if err := tdl.WriteSelectionJSON(selPath, task.PeerID, task.Selected); err != nil {
			log.Printf("task %s build selection failed: %v", task.ID, err)
			cancel()
			m.failTask(task, r, "生成下载清单失败: "+err.Error())
			return
		}
		srcFile = selPath
		log.Printf("task %s source: selection peer=%d messages=%d file=%s save=%s",
			task.ID, task.PeerID, len(task.Selected), selPath, task.SavePath)
	}

	// 整频道链接按链接形态兜底识别：创建时若因下载占锁跳过了探测，
	// 频道链接的 kind 会是占位的 single/album
	_, _, isChannelLink := tdl.ChannelLink(task.URL)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		if srcFile == "" && (task.Kind == "channel" || isChannelLink) {
			// 整频道任务：先导出频道媒体消息清单（serve 与下载共用）
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
			srcFile = exportPath
		}
		m.runServeTask(task, r, ctx, srcURL, srcFile)
	}()
}

// ServeBusy 报告 tdl bolt 存储锁是否可能被占（存在活动任务：serve 下载、
// 频道导出或排队等待中）。期间任何 tdl 进程（探测/资源枚举等）都会在
// flock 上阻塞，HTTP 接口必须避免在此状态下同步调用 tdl。
func (m *Manager) ServeBusy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running) > 0
}

// errStopped 内部哨兵：任务被暂停/取消终止（非错误，不落失败状态）。
var errStopped = errors.New("task stopped")

// runServeTask serve+aria2 下载流程：
//  1. 启动 tdl dl --serve，等待索引就绪并获取文件清单（真实文件名+大小）
//  2. 逐文件调用 aria2c -c 下载（<名字>.aria2 控制文件记录断点）
//  3. 暂停/取消：终止 aria2 与 serve，保留控制文件 → 恢复时字节级续传
//  4. 失败：单文件自动重试 3 次，仍失败则整个任务失败（控制文件保留）
//
// serve 进程独占 tdl bolt 存储锁，任务在 m.serveGate 上排队串行执行。
func (m *Manager) runServeTask(task *Task, r *runner, ctx context.Context, srcURL, srcFile string) {
	cfg := m.opts.Settings.Get()

	// bolt 锁互斥：并发数大于 1 时其余任务在此排队
	select {
	case m.serveGate <- struct{}{}:
		defer func() { <-m.serveGate }()
	case <-ctx.Done():
		close(r.stopped)
		return
	}

	// 排队期间任务可能已被暂停/取消
	m.mu.Lock()
	st := task.Status
	m.mu.Unlock()
	if st != StatusDownloading {
		close(r.stopped)
		return
	}

	// 创建时正有任务在下载（bolt 被占）而跳过了探测的任务，在此补一次：
	// 回填任务名/频道/类型/大小等展示信息。持锁状态下 bolt 空闲，探测
	// 不会阻塞；探测失败不阻塞下载（私有链接属预期，tdl 下载时兜底）。
	m.mu.Lock()
	needProbe := task.ChatTitle == "" && len(task.Selected) == 0 && task.URL != ""
	m.mu.Unlock()
	if needProbe {
		pctx, pcancel := context.WithTimeout(ctx, 60*time.Second)
		for _, pr := range tdl.Probe(pctx, tdl.ClientConfig{
			DataDir:   m.opts.TdlDataDir,
			Namespace: m.opts.Namespace,
			Proxy:     cfg.Proxy,
			AppID:     cfg.AppID,
			AppHash:   cfg.AppHash,
		}, []string{task.URL}) {
			if pr.Error != "" {
				log.Printf("task %s late probe: %s", task.ID, pr.Error)
				continue
			}
			m.mu.Lock()
			task.ChatTitle = pr.ChatTitle
			task.MessageID = pr.MessageID
			task.Kind = pr.Kind
			if pr.Title != "" {
				task.Name = pr.Title
			}
			if pr.TotalSize > 0 {
				task.TotalSize = pr.TotalSize
			}
			m.persistLocked()
			m.mu.Unlock()
			log.Printf("task %s late probe ok: kind=%s title=%s save=%s",
				task.ID, pr.Kind, pr.Title, task.SavePath)
		}
		pcancel()
	}

	// 保存目录对齐：创建时因占锁跳过探测（或旧版本探测未纠正目录）的
	// 任务，子目录可能是链接回退名（如频道 username），与磁盘上已有
	// 断点文件的频道名目录对不上。这里统一按频道名纠正；目录非空
	// （已开始下载）则不动。
	m.mu.Lock()
	if task.ChatTitle != "" {
		if sub := tdl.DirNameForURL(task.URL, task.ChatTitle); sub != "" {
			newPath := filepath.Join(filepath.Dir(task.SavePath), sub)
			if newPath != task.SavePath {
				if ents, e := os.ReadDir(task.SavePath); e != nil || len(ents) == 0 {
					log.Printf("task %s save path realigned: %s -> %s", task.ID, task.SavePath, newPath)
					task.SavePath = newPath
					m.persistLocked()
				}
			}
		}
	}
	m.mu.Unlock()

	ariaBin := m.aria2Bin()
	if ariaBin == "" {
		m.failTask(task, r, "未找到 aria2c 可执行文件（字节级续传下载器），请检查安装")
		return
	}

	onLog := func(line string) { log.Printf("tdl[%s] %s", task.ID, line) }

	// tdl serve 不展开相册（--group 仅作用于内置下载器）：相册链接先
	// 用资源枚举自行展开为消息清单，转 -f 喂给 serve。失败时回落单条
	// 下载，不阻塞任务。
	if srcFile == "" && task.Group {
		set := tdl.ListResources(ctx, tdl.ClientConfig{
			DataDir:   m.opts.TdlDataDir,
			Namespace: m.opts.Namespace,
			Proxy:     cfg.Proxy,
			AppID:     cfg.AppID,
			AppHash:   cfg.AppHash,
		}, srcURL)
		if set.Error == "" && (set.Kind == "album" || set.Kind == "group") && set.PeerID != 0 && len(set.Resources) > 1 {
			ids := make([]int, 0, len(set.Resources))
			for _, res := range set.Resources {
				ids = append(ids, res.MsgID)
			}
			albumPath := filepath.Join(m.opts.DataDir, "exports", task.ID+".album.json")
			if err := tdl.WriteSelectionJSON(albumPath, set.PeerID, ids); err == nil {
				srcFile = albumPath
				log.Printf("task %s album expanded: peer=%d messages=%d", task.ID, set.PeerID, len(ids))
			} else {
				log.Printf("task %s album expand failed: %v", task.ID, err)
			}
		}
	}

	proc, files, err := tdl.StartServe(ctx, tdl.ServeOptions{
		TdlPath:   m.opts.TdlPath,
		DataDir:   m.opts.TdlDataDir,
		Namespace: m.opts.Namespace,
		Proxy:     cfg.Proxy,
		URL:       srcURL,
		File:      srcFile,
		Group:     task.Group && srcFile == "", // 清单已展开相册，仅链接来源需要 --group
	}, onLog)
	if err != nil {
		m.failTask(task, r, "启动下载服务失败: "+err.Error())
		return
	}
	r.fm.Lock()
	r.serve = proc
	r.fm.Unlock()
	defer proc.Stop()
	log.Printf("task %s serve ready: port=%d endpoints=%d save=%s",
		task.ID, proc.Port(), len(files), task.SavePath)

	// 过滤元数据缺失的端点（HEAD 失败的极端情况）
	plan0 := make([]tdl.ServeFile, 0, len(files))
	for _, f := range files {
		if f.Name != "" {
			plan0 = append(plan0, f)
		}
	}
	if len(plan0) == 0 {
		m.failTask(task, r, "未解析到可下载的文件（消息可能不包含媒体）")
		return
	}

	// 重名消解：目标位置已存在同名文件（无控制文件）且大小不符时，
	// 改用「消息ID_文件名」，避免 aria2 报 File already exists 后原地打转
	type dlFile struct {
		sf   tdl.ServeFile
		name string
	}
	plan := make([]dlFile, 0, len(plan0))
	var total int64
	for _, f := range plan0 {
		name := f.Name
		if _, ctrlErr := os.Stat(filepath.Join(task.SavePath, name+".aria2")); ctrlErr != nil {
			if st, err := os.Stat(filepath.Join(task.SavePath, name)); err == nil && st.Size() != f.Size {
				name = fmt.Sprintf("%d_%s", f.MsgID, name)
			}
		}
		plan = append(plan, dlFile{sf: f, name: name})
		total += f.Size
	}
	// 供取消清理用：名字以最终计划名为准
	finalFiles := make([]tdl.ServeFile, len(plan))
	for i, p := range plan {
		finalFiles[i] = p.sf
		finalFiles[i].Name = p.name
	}
	r.fm.Lock()
	r.serveFiles = finalFiles
	r.fm.Unlock()

	// 文件清单落任务（真实文件名+大小），保留同名旧条目 ID 减少前端抖动
	m.mu.Lock()
	oldByName := make(map[string]File, len(task.Files))
	for _, of := range task.Files {
		oldByName[of.Name] = of
	}
	newFiles := make([]File, 0, len(plan))
	for _, p := range plan {
		id := newID()
		if of, ok := oldByName[p.name]; ok {
			id = of.ID
		}
		newFiles = append(newFiles, File{ID: id, Name: p.name, Size: p.sf.Size, Status: StatusPending})
	}
	task.Files = newFiles
	if total > 0 {
		task.TotalSize = total
	}
	m.persistLocked()
	m.mu.Unlock()

	// onLine：记录当前文件百分比与瞬时速率（aria2 摘要行）
	var curIdx int
	onLine := func(line string) {
		if p, ok := tdl.ParseAria2Line(line); ok {
			r.fm.Lock()
			if r.filePerc == nil {
				r.filePerc = make(map[string]float64)
			}
			r.ariaPct = p.Percent
			r.filePerc[plan[curIdx].sf.Endpoint] = p.Percent
			if p.Speed > 0 && float64(p.Speed) > r.speed {
				r.speed = float64(p.Speed)
			}
			r.fm.Unlock()
		}
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	completedBytes := int64(0)

	for idx := range plan {
		p := plan[idx]

		// 终止检查（暂停/取消后立即退出，保留断点）
		select {
		case <-r.stop:
			close(r.stopped)
			return
		default:
		}
		m.mu.Lock()
		if task.Status != StatusDownloading {
			m.mu.Unlock()
			close(r.stopped)
			return
		}
		// 已完整的文件直接标记完成（重试/恢复场景秒级跳过）
		finalPath := filepath.Join(task.SavePath, p.name)
		if _, ctrlErr := os.Stat(finalPath + ".aria2"); ctrlErr != nil && p.sf.Size > 0 {
			if st, err := os.Stat(finalPath); err == nil && st.Size() == p.sf.Size {
				task.Files[idx].Status = StatusCompleted
				task.Files[idx].Progress = 100
				m.persistLocked()
				m.mu.Unlock()
				completedBytes += p.sf.Size
				continue
			}
		}
		task.Files[idx].Status = StatusDownloading
		m.persistLocked()
		m.mu.Unlock()

		r.fm.Lock()
		curIdx = idx
		r.ariaPct = 0
		r.fm.Unlock()

		var lastErr error
		var aria *tdl.RunningDownload
		for attempt := 1; attempt <= 3; attempt++ {
			aria, err = tdl.StartAria2(ctx, tdl.Aria2Options{
				Aria2Path: ariaBin,
				URL:       proc.URL(p.sf.Endpoint),
				Dir:       task.SavePath,
				Out:       p.name,
			}, onLine, onLog)
			if err != nil {
				lastErr = err // 启动失败（路径/权限问题）重试无意义
				break
			}
			r.fm.Lock()
			r.dl = aria // 让 Pause/Cancel 能终止它
			r.fm.Unlock()

			// 等待下载完成 / 终止 / 每秒推进度
		waitLoop:
			for {
				select {
				case lastErr = <-aria.Done:
					break waitLoop
				case <-r.stop:
					aria.Stop() // 优雅终止：aria2 保存 .aria2 控制文件
					select {
					case <-aria.Done:
					case <-time.After(4 * time.Second):
					}
					lastErr = errStopped
					break waitLoop
				case <-tick.C:
					m.applyServeProgress(task, r, idx, completedBytes, total)
				}
			}
			r.fm.Lock()
			r.dl = nil
			r.fm.Unlock()

			// 暂停/取消会先置任务状态再停进程：aria2 被信号终止时的退出码
			// 不可靠（优雅退出可能送达 0），所以退出后先看任务状态，
			// 再以磁盘实况（大小匹配且无 .aria2 控制文件）判定成败。
			m.mu.Lock()
			active := task.Status == StatusDownloading
			m.mu.Unlock()
			if lastErr == errStopped || !active {
				close(r.stopped)
				return
			}
			if p.sf.Size > 0 {
				if st, statErr := os.Stat(finalPath); statErr == nil && st.Size() == p.sf.Size {
					if _, ctrlErr := os.Stat(finalPath + ".aria2"); ctrlErr != nil {
						lastErr = nil // 实际已完整，忽略可疑退出码
					}
				} else if lastErr == nil {
					lastErr = errors.New("aria2 提前退出，文件不完整")
				}
			}

			if lastErr == nil {
				break
			}
			log.Printf("task %s file %s aria2 attempt %d failed: %v", task.ID, p.name, attempt, lastErr)
			select {
			case <-r.stop:
				lastErr = errStopped
			case <-ctx.Done():
				lastErr = errStopped
			case <-time.After(2 * time.Second):
			}
			if lastErr == errStopped {
				break
			}
		}

		if lastErr == errStopped {
			// 暂停/取消：.aria2 控制文件已保留，恢复时字节级续传
			close(r.stopped)
			return
		}
		if lastErr != nil {
			msg := fmt.Sprintf("下载 %s 失败: %s", p.name, trimErr(lastErr))
			if aria != nil {
				if tail := aria.LogTail(); tail != "" {
					msg += "（aria2: " + tail + "）"
				}
			}
			m.failTask(task, r, msg)
			return
		}
		completedBytes += p.sf.Size
		m.mu.Lock()
		task.Files[idx].Status = StatusCompleted
		task.Files[idx].Progress = 100
		task.Files[idx].Downloaded = p.sf.Size
		m.persistLocked()
		m.mu.Unlock()
	}

	// 全部完成
	m.mu.Lock()
	task.Status = StatusCompleted
	task.Progress = 100
	task.Speed = 0
	for i := range task.Files {
		task.Files[i].Status = StatusCompleted
		task.Files[i].Progress = 100
		if task.Files[i].Size > 0 {
			task.Files[i].Downloaded = task.Files[i].Size
		}
	}
	var sum int64
	for _, f := range task.Files {
		sum += f.Size
	}
	if sum > task.DownloadedSize {
		task.DownloadedSize = sum
	}
	if sum > task.TotalSize {
		task.TotalSize = sum
	}
	task.DownloadedSize = sum
	task.FinishedAt = time.Now().UnixMilli()
	delete(m.running, task.ID)
	m.persistLocked()
	m.pumpLocked()
	m.mu.Unlock()
	log.Printf("task %s completed: %d files, %d bytes", task.ID, len(plan), sum)
	close(r.stopped)
}

// applyServeProgress 每秒由下载等待循环调用：把 aria2 当前文件进度
// 折算成任务总进度（已完成字节 + 当前文件百分比 × 文件大小）。
func (m *Manager) applyServeProgress(task *Task, r *runner, idx int, completedBytes, total int64) {
	r.fm.Lock()
	pct := r.ariaPct
	r.fm.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if task.Status != StatusDownloading || idx >= len(task.Files) {
		return
	}
	curBytes := task.Files[idx].Size * int64(pct) / 100
	dl := completedBytes + curBytes
	if dl > task.DownloadedSize {
		task.DownloadedSize = dl
	}
	// 速度用进度差分（aria2 的 DL 字段每秒一拍，与 tick 错拍会采到 0）
	r.fm.Lock()
	delta := dl - r.ariaLastDl
	if delta < 0 {
		delta = 0
	}
	r.ariaLastDl = dl
	r.fm.Unlock()
	task.Speed = int64(0.5*float64(task.Speed) + 0.5*float64(delta))
	task.Files[idx].Downloaded = curBytes
	task.Files[idx].Progress = pct
	if total > 0 {
		p := float64(dl) / float64(total) * 100
		if p > 99 {
			p = 99
		}
		if p < 0 {
			p = 0
		}
		task.Progress = p
	}
	m.persistLocked()
}

// aria2Bin 解析 aria2c 可执行文件：优先配置路径，其次 PATH。
func (m *Manager) aria2Bin() string {
	if m.opts.Aria2Path != "" {
		if _, err := os.Stat(m.opts.Aria2Path); err == nil {
			return m.opts.Aria2Path
		}
	}
	if p, err := exec.LookPath("aria2c"); err == nil {
		return p
	}
	return ""
}

// removeServePartials 删除 serve 下载路径的半成品与 .aria2 控制文件；
// 已完整下载（大小匹配且无控制文件）的文件保留。
func removeServePartials(dir string, files []tdl.ServeFile) {
	if dir == "" {
		return
	}
	for _, f := range files {
		if f.Name == "" {
			continue
		}
		fp := filepath.Join(dir, f.Name)
		ctrl := fp + ".aria2"
		_, ctrlErr := os.Stat(ctrl)
		st, err := os.Stat(fp)
		switch {
		case ctrlErr == nil:
			// 有控制文件 → 未完成，删除本体与控制文件
			_ = os.Remove(fp)
			_ = os.Remove(ctrl)
		case err == nil && f.Size > 0 && st.Size() != f.Size:
			// 无控制文件但大小不符（中断残留），删除
			_ = os.Remove(fp)
		}
	}
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

// stopProc 终止 runner 当前子进程（aria2/下载/导出）并等待其退出，
// 最后终止 tdl serve 进程（serve 无优雅退出需求，直接 Kill）。
func (r *runner) stopProc() {
	r.fm.Lock()
	dl, ex, serve := r.dl, r.export, r.serve
	r.fm.Unlock()
	if dl != nil {
		dl.Stop()
		select {
		case <-dl.Done:
		case <-time.After(6 * time.Second):
		}
	}
	if serve != nil {
		serve.Stop()
	}
	if ex != nil {
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
