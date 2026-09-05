package tdl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServeOptions 描述一个 tdl dl --serve 子进程：把待下载文件以本地 HTTP
// 端点暴露（支持 Range/206），由 aria2c 等下载器完成真正的传输。
// 相比 tdl 内置下载器，这条路径可获得字节级断点续传。
type ServeOptions struct {
	TdlPath   string
	DataDir   string
	Namespace string
	Proxy     string

	// 与 DownloadOptions 相同的二选一来源：
	// File 为 selection/export JSON 路径；URL 为消息/频道链接
	URL  string
	File string
	// Group 相册/组合消息展开（tdl --group）
	Group bool

	// Port 本地监听端口；0 表示自动挑选空闲端口
	Port int
}

// ServeFile serve 索引暴露的一个可下载文件。
type ServeFile struct {
	Endpoint string // URL 路径段，如 "4434014100/2"
	PeerID   int64  // 数字会话 ID（非数字端点为 0）
	MsgID    int64  // 消息 ID
	Name     string // 真实文件名（Content-Disposition）
	Size     int64  // 文件字节数（Content-Length）
}

// ServeProc 一个运行中的 tdl serve 进程。
type ServeProc struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	port   int

	// Done 在进程退出后返回其错误
	Done chan error
}

// Port 返回本地监听端口。
func (s *ServeProc) Port() int { return s.port }

// URL 返回某个端点的完整下载地址。
func (s *ServeProc) URL(endpoint string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s", s.port, strings.TrimPrefix(endpoint, "/"))
}

// Stop 温和终止 serve 进程。
func (s *ServeProc) Stop() {
	if s == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Kill()
}

// serveEndpointsRe 从 serve 索引页提取端点（href，如 "4434014100/2"）。
var serveEndpointsRe = regexp.MustCompile(`<a href="([^"]+)"`)

// serveFreePort 挑选一个空闲 TCP 端口（存在极小概率竞态，可接受）。
func serveFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ServeArgs 构建 tdl dl --serve 命令行参数。
func ServeArgs(o ServeOptions) []string {
	args := []string{
		"dl",
		"--serve",
		"--port", strconv.Itoa(o.Port),
		"-n", o.Namespace,
		"--storage", fmt.Sprintf("type=bolt,path=%s", o.DataDir),
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
	return args
}

// StartServe 启动 tdl dl --serve，等待端口就绪并解析文件清单。
// onLog 接收 serve 进程的非空输出行（落服务日志排错）。
func StartServe(ctx context.Context, o ServeOptions, onLog func(string)) (*ServeProc, []ServeFile, error) {
	if o.Port == 0 {
		p, err := serveFreePort()
		if err != nil {
			return nil, nil, fmt.Errorf("pick port: %w", err)
		}
		o.Port = p
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, o.TdlPath, ServeArgs(o)...)
	cmd.SysProcAttr = hideWindowAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start tdl serve: %w", err)
	}

	proc := &ServeProc{
		cmd:    cmd,
		cancel: cancel,
		port:   o.Port,
		Done:   make(chan error, 1),
	}
	pumpLog := func(rd io.ReadCloser) {
		if rd == nil {
			return
		}
		defer rd.Close()
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			for _, seg := range strings.Split(sc.Text(), "\r") {
				line := strings.TrimSpace(ansiRe.ReplaceAllString(seg, ""))
				if line == "" {
					continue
				}
				if onLog != nil {
					onLog(line)
				}
			}
		}
	}
	go pumpLog(stdout)
	go pumpLog(stderr)
	go func() {
		err := cmd.Wait()
		if cmdCtx.Err() != nil && err != nil {
			err = nil
		}
		proc.Done <- err
		close(proc.Done)
		cancel()
	}()

	// 轮询索引直至就绪（serve 需先解析消息，经代理时可能要十几秒）
	base := fmt.Sprintf("http://127.0.0.1:%d", o.Port)
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	var indexHTML string
	for {
		select {
		case err := <-proc.Done:
			if err != nil {
				err = fmt.Errorf("serve 进程提前退出: %v", err)
			} else {
				err = fmt.Errorf("serve 进程提前退出")
			}
			return nil, nil, err
		default:
		}
		resp, err := client.Get(base + "/")
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				indexHTML = string(body)
				break
			}
		}
		if time.Now().After(deadline) {
			proc.Stop()
			return nil, nil, fmt.Errorf("serve 端口 %d 等待就绪超时", o.Port)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 解析端点并逐个取元数据（文件名 + 大小），并发 8 路加速大频道
	hrefs := serveEndpointsRe.FindAllStringSubmatch(indexHTML, -1)
	endpoints := make([]string, 0, len(hrefs))
	seen := make(map[string]bool, len(hrefs))
	for _, m := range hrefs {
		ep := strings.Trim(m[1], "/")
		if ep == "" || seen[ep] {
			continue
		}
		seen[ep] = true
		endpoints = append(endpoints, ep)
	}
	if len(endpoints) == 0 {
		proc.Stop()
		return nil, nil, fmt.Errorf("serve 索引为空（消息可能不包含媒体）")
	}

	files := make([]ServeFile, len(endpoints))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	headClient := &http.Client{Timeout: 15 * time.Second}
	for i, ep := range endpoints {
		wg.Add(1)
		go func(i int, ep string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			f := ServeFile{Endpoint: ep}
			f.PeerID, f.MsgID = parseEndpointIDs(ep)
			// HEAD 会触发 serve 端实时解析媒体元数据（经代理可能要数秒），
			// 超时重试；仍失败则给端点兜底名，绝不丢文件
			for attempt := 0; attempt < 3; attempt++ {
				if name, size, ok := serveHead(headClient, base+"/"+ep); ok {
					f.Name, f.Size = name, size
					break
				}
				time.Sleep(time.Second)
			}
			if f.Name == "" {
				f.Name = fmt.Sprintf("%d_%d_download", f.PeerID, f.MsgID)
			}
			files[i] = f
		}(i, ep)
	}
	wg.Wait()
	return proc, files, nil
}

// parseEndpointIDs 从端点路径提取数字会话/消息 ID（非数字返回 0）。
func parseEndpointIDs(ep string) (int64, int64) {
	parts := strings.SplitN(ep, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	peer, _ := strconv.ParseInt(parts[0], 10, 64)
	msg, _ := strconv.ParseInt(parts[1], 10, 64)
	return peer, msg
}

// contentDispRe 匹配 Content-Disposition 的 filename 参数。
var contentDispRe = regexp.MustCompile(`filename\*?=(?:UTF-8''|")?([^";]+)"?`)

// serveHead 请求端点头部，解析真实文件名与大小。
func serveHead(client *http.Client, rawURL string) (string, int64, bool) {
	resp, err := client.Head(rawURL)
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", 0, false
	}
	cd := resp.Header.Get("Content-Disposition")
	name := ""
	if m := contentDispRe.FindStringSubmatch(cd); m != nil {
		name, _ = url.PathUnescape(strings.TrimSpace(m[1]))
	}
	return name, resp.ContentLength, name != "" && resp.ContentLength > 0
}
