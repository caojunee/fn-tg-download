package tdl

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Aria2Options 描述一个 aria2c 子进程：从 tdl serve 的本地 HTTP 端点
// 下载单个文件。-c 断点续传依赖 <Out>.aria2 控制文件。
type Aria2Options struct {
	Aria2Path string
	// URL 为 tdl serve 的本地端点地址（http://127.0.0.1:port/<endpoint>）
	URL string
	Dir string
	Out string // 保存文件名
}

// Aria2Progress aria2 控制台摘要行解析结果。
type Aria2Progress struct {
	Downloaded int64
	Total      int64
	Percent    float64
	Speed      int64 // 字节/秒
}

// aria2Size 单位换算（KiB/MiB/GiB → 字节）。
func aria2Size(num float64, unit string) int64 {
	switch unit {
	case "K":
		return int64(num * 1024)
	case "M":
		return int64(num * 1024 * 1024)
	case "G":
		return int64(num * 1024 * 1024 * 1024)
	default:
		return int64(num)
	}
}

// aria2Re 匹配 aria2 控制台摘要行：
// [#8b3f9a 20MiB/80MiB(25%) CN:1 DL:4.2MiB ETA:15s]
var aria2Re = regexp.MustCompile(
	`^\[#[0-9a-zA-Z]+ ([0-9.]+)([KMGT]?)i?B/([0-9.]+)([KMGT]?)i?B\((\d+(?:\.\d+)?)%\)` +
		`(?: CN:\d+)?(?: DL:([0-9.]+)([KMGT]?)i?B(?:/s)?)?`)

// ParseAria2Line 解析 aria2 进度摘要行。
func ParseAria2Line(line string) (Aria2Progress, bool) {
	m := aria2Re.FindStringSubmatch(line)
	if m == nil {
		return Aria2Progress{}, false
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	t, _ := strconv.ParseFloat(m[3], 64)
	p, _ := strconv.ParseFloat(m[5], 64)
	var speed int64
	if m[6] != "" {
		s, _ := strconv.ParseFloat(m[6], 64)
		speed = aria2Size(s, m[7])
	}
	return Aria2Progress{
		Downloaded: aria2Size(n, m[2]),
		Total:      aria2Size(t, m[4]),
		Percent:    p,
		Speed:      speed,
	}, true
}

// Aria2Args 构建 aria2c 命令行参数。
func Aria2Args(o Aria2Options) []string {
	return []string{
		"-c", // 断点续传：<Out>.aria2 控制文件
		// 关闭预分配：文件随下载增长，目录差分速度统计才准确
		"--file-allocation=none",
		// 同名文件不自动改名/覆盖：重名冲突交给上层显式处理
		"--auto-file-renaming=false",
		"--allow-overwrite=false",
		"--console-log-level=warn",
		"--summary-interval=1",
		"--retry-wait=2",
		"--max-tries=5",
		"--connect-timeout=15",
		"--timeout=30",
		// 多连接分段下载：tdl serve 原生支持 Range/206，单连接只有一条
		// Telegram 拉取流（实测 ~700KB/s），16 连接可并行拉满带宽（~3.1MB/s+）
		"--max-connection-per-server=16",
		"--split=16",
		"--min-split-size=4M",
		"-d", o.Dir,
		"-o", o.Out,
		o.URL,
	}
}

// StartAria2 启动 aria2c 子进程。onLine 接收进度摘要行，onLog 接收
// 非进度行（警告/错误）。注意：aria2 会尊重 http_proxy 等环境变量，
// 而 URL 是本机 serve 端点，必须直连——启动时清除代理环境变量。
func StartAria2(ctx context.Context, o Aria2Options, onLine, onLog func(string)) (*RunningDownload, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, o.Aria2Path, Aria2Args(o)...)
	cmd.SysProcAttr = hideWindowAttr()
	// aria2 走本机 serve，绝不代理
	cleanEnv := make([]string, 0, len(cmd.Env)+1)
	for _, e := range cmd.Env {
		ue := strings.ToUpper(e)
		if strings.HasPrefix(ue, "HTTP_PROXY=") || strings.HasPrefix(ue, "HTTPS_PROXY=") ||
			strings.HasPrefix(ue, "ALL_PROXY=") || strings.HasPrefix(ue, "http_proxy=") ||
			strings.HasPrefix(ue, "https_proxy=") || strings.HasPrefix(ue, "all_proxy=") {
			continue
		}
		cleanEnv = append(cleanEnv, e)
	}
	cmd.Env = cleanEnv

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
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start aria2c: %w", err)
	}

	r := &RunningDownload{
		cmd:    cmd,
		Done:   make(chan error, 1),
		Cancel: cancel,
		onLine: onLine,
		onLog:  onLog,
		// aria2 摘要行走进度通道；其余输出（警告/错误）转 onLog
		classify: func(line string) bool {
			_, ok := ParseAria2Line(line)
			return ok
		},
	}
	go r.pump(stdout)
	go r.pump(stderr)
	go func() {
		err := cmd.Wait()
		if cmdCtx.Err() != nil && err != nil {
			err = nil
		}
		r.Done <- err
		close(r.Done)
		cancel()
	}()
	return r, nil
}
