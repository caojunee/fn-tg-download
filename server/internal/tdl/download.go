package tdl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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
		"-u", o.URL,
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
}

// Start 启动 tdl dl 子进程，onLine 会在每读取到一行 stdout/stderr 时被调用。
func Start(ctx context.Context, o DownloadOptions, onLine func(string)) (*RunningDownload, error) {
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
			line := strings.TrimSpace(seg)
			if line != "" && r.onLine != nil {
				r.onLine(line)
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
