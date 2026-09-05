package tdl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DownloadOptions 描述一个 tdl dl 子进程的参数。
type DownloadOptions struct {
	TdlPath   string
	DataDir   string
	Namespace string
	Proxy     string

	URL      string
	// File 为 tdl chat export 导出的 JSON 路径；非空时以 -f 模式下载
	// （整频道任务），忽略 URL。
	File     string
	SavePath string
	Group    bool

	// 进程内并发任务数（tdl -l）
	Limit int
	// 单文件线程数（tdl -t）
	Threads int

	Include []string
	Exclude []string
}

// BuildArgs 构建 tdl dl 命令行参数。
func BuildArgs(o DownloadOptions) []string {
	args := []string{
		"dl",
		"-d", o.SavePath,
		// 恢复下载且不交互（tdl 有未完成进度时直接续传）
		"--continue",
		// 文件名一致且大小一致时自动跳过
		"--skip-same",
		"-n", o.Namespace,
		"--storage", fmt.Sprintf("type=bolt,path=%s", o.DataDir),
		"-l", strconv.Itoa(o.Limit),
		"-t", strconv.Itoa(o.Threads),
	}
	if o.File != "" {
		args = append(args, "-f", o.File)
	} else {
		args = append(args, "-u", o.URL)
	}
	if o.Group {
		args = append(args, "--group")
	}
	if strings.TrimSpace(o.Proxy) != "" {
		args = append(args, "--proxy", o.Proxy)
	}
	if len(o.Include) > 0 {
		args = append(args, "-i", strings.Join(o.Include, ","))
	}
	if len(o.Exclude) > 0 {
		args = append(args, "-e", strings.Join(o.Exclude, ","))
	}
	return args
}

// RunningDownload 一个运行中的下载子进程。
type RunningDownload struct {
	cmd *exec.Cmd
	// Done 在进程退出后返回其错误（含退出码信息）
	Done chan error
	// Cancel 请求终止进程：先 SIGTERM（Linux 下 tdl 会保存断点），
	// 3 秒后仍未退出则强杀。
	Cancel func()

	onLine func(string)
	// onLog 接收 tdl 子进程输出中的非进度行（错误/警告/诊断信息），
	// 写入服务日志，便于点击下载失败时定位真实原因（进度刷新行不记，避免刷屏）。
	onLog func(string)

	// logMu/logTail 收集最近若干条非进度行，任务失败时附加到错误信息，
	// 让前端直接看到 tdl 的真实报错（exit status 2 = Go panic 之类）。
	logMu   sync.Mutex
	logTail []string
}

// logNoiseRe 匹配进度渲染器的噪声行：CPU/内存统计行与进度条行。
// 这些行每帧（100ms）都会输出但不含 tracker 标记，ParseProgressLine
// 判为非进度行，若不显式排除会把日志刷爆。进度条 0% 时全为 '.'，
// 已下载时含 '#'，字符类须同时覆盖（[#.]+ 不能只匹配 # 开头）。
var logNoiseRe = regexp.MustCompile(`^CPU:.*Memory:.*Goroutines:|\[[#.]+\]`)

// logTailMax 保留的最近非进度行数（失败信息附加用）。
const logTailMax = 10

// LogTail 返回最近的非进度输出（单行拼接，供错误信息附加）。
func (r *RunningDownload) LogTail() string {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if len(r.logTail) == 0 {
		return ""
	}
	return strings.Join(r.logTail, " | ")
}

// Start 启动 tdl dl 子进程，onLine 会在每读取到一行 stdout/stderr 时被调用。
// onLog 仅在非进度行（错误/警告/诊断）时被调用，用于排错日志，避免进度刷新刷屏。
func Start(ctx context.Context, o DownloadOptions, onLine, onLog func(string)) (*RunningDownload, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, o.TdlPath, BuildArgs(o)...)
	cmd.Stdout = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	// 平台相关的进程属性（Windows 隐藏控制台窗口；Unix 设置进程组）
	cmd.SysProcAttr = hideWindowAttr()

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start tdl: %w", err)
	}

	r := &RunningDownload{
		cmd:    cmd,
		Done:   make(chan error, 1),
		Cancel: cancel,
		onLine: onLine,
		onLog:  onLog,
	}

	go r.pump(stdout)
	go r.pump(stderr)

	go func() {
		err := cmd.Wait()
		// 上下文取消导致的退出视为「已停止」，由调用方根据自身状态解释
		if cmdCtx.Err() != nil && (err != nil) {
			err = nil
		}
		r.Done <- err
		close(r.Done)
		cancel()
	}()

	return r, nil
}

func (r *RunningDownload) pump(rd io.ReadCloser) {
	if rd == nil {
		return
	}
	defer rd.Close()
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		// 进度条用 \r 原地刷新，一帧内含多个 tracker 行，
		// 需按 \r 再切分，否则整帧粘成一行无法解析
		for _, seg := range strings.Split(sc.Text(), "\r") {
			line := strings.TrimSpace(ansiRe.ReplaceAllString(seg, ""))
			if line == "" {
				continue
			}
			if r.onLine != nil {
				r.onLine(line)
			}
			// 非进度行（错误/警告/panic 堆栈/tdl 日志）转发给 onLog 并
			// 存入尾部缓冲；进度渲染噪声行（CPU 统计、进度条）与 tracker
			// 进度行排除，避免刷屏
			if _, ok := ParseProgressLine(line); ok || logNoiseRe.MatchString(line) {
				continue
			}
			r.logMu.Lock()
			r.logTail = append(r.logTail, line)
			if len(r.logTail) > logTailMax {
				r.logTail = r.logTail[len(r.logTail)-logTailMax:]
			}
			r.logMu.Unlock()
			if r.onLog != nil {
				r.onLog(line)
			}
		}
	}
}

// Stop 温和终止：SIGTERM → 3s → Kill。
func (r *RunningDownload) Stop() {
	if r.cmd.Process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			select {
			case <-r.Done:
			case <-time.After(3 * time.Second):
				_ = r.cmd.Process.Kill()
			}
		}()
		return
	}
	// Windows 无优雅信号，直接 Kill
	_ = r.cmd.Process.Kill()
}

// Pid 返回子进程 PID（调试用）。
func (r *RunningDownload) Pid() int {
	if r.cmd.Process == nil {
		return 0
	}
	return r.cmd.Process.Pid
}
