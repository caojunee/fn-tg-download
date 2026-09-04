package tdl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// chanRe 匹配整频道链接（不含消息 ID）：
//
//	https://t.me/<username>
//	https://t.me/c/<internal_id>
//	允许尾随斜杠与查询参数（?topic=xx 等）。
var chanRe = regexp.MustCompile(`^(?:https?://)?t\.me/(?:c/(\d+)|([A-Za-z0-9_]+))/?(?:\?.*)?$`)

// ChannelLink 判断链接是否为整频道链接。
// 返回 chat 标识（私有频道为数字 ID，公开频道为用户名）、是否私有、是否命中。
func ChannelLink(raw string) (chat string, private bool, ok bool) {
	m := chanRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", false, false
	}
	if m[1] != "" {
		return m[1], true, true
	}
	return m[2], false, true
}

// ExportOptions 描述一个 tdl chat export 子进程的参数。
type ExportOptions struct {
	TdlPath   string
	DataDir   string // tdl bolt 存储目录
	Namespace string
	Proxy     string

	Chat   string // 数字 ID 或用户名
	Output string // 导出 JSON 路径
}

// BuildExportArgs 构建 tdl chat export 命令行参数。
// --with-content 附带文件名，--raw 附带媒体元数据（取文件大小用）。
func BuildExportArgs(o ExportOptions) []string {
	args := []string{
		"chat", "export",
		"-c", o.Chat,
		"--with-content",
		"--raw",
		"-o", o.Output,
		"-n", o.Namespace,
		"--storage", fmt.Sprintf("type=bolt,path=%s", o.DataDir),
	}
	if strings.TrimSpace(o.Proxy) != "" {
		args = append(args, "--proxy", o.Proxy)
	}
	return args
}

// RunningExport 一个运行中的导出子进程。
type RunningExport struct {
	cmd  *exec.Cmd
	Done chan error
}

// StartExport 启动 tdl chat export 子进程。
// 导出目录不存在时自动创建；失败时错误信息会附带子进程 stderr（截断）。
func StartExport(ctx context.Context, o ExportOptions) (*RunningExport, error) {
	if err := os.MkdirAll(filepath.Dir(o.Output), 0o755); err != nil {
		return nil, fmt.Errorf("create export dir: %w", err)
	}
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, o.TdlPath, BuildExportArgs(o)...)
	// 导出进度条输出无解析价值，丢弃；stderr 留作失败原因诊断
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = hideWindowAttr()

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start tdl export: %w", err)
	}

	re := &RunningExport{cmd: cmd, Done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		if cmdCtx.Err() != nil && err != nil {
			err = nil // 主动终止视为已停止
		}
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				if len(msg) > 400 {
					msg = msg[len(msg)-400:] // 保留尾部（错误一般在最后）
				}
				err = fmt.Errorf("%w: %s", err, msg)
			}
		}
		re.Done <- err
		close(re.Done)
		cancel()
	}()
	return re, nil
}

// Stop 终止导出子进程（SIGTERM → 3s → Kill，与下载一致）。
func (r *RunningExport) Stop() {
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
	_ = r.cmd.Process.Kill()
}

// ExportFile 导出文件中解析出的单个媒体文件。
// Key 为 "对话ID_消息ID"，与磁盘文件名模板 {对话ID}_{消息ID}_{文件名} 的
// 前两段一致，用于任务文件列表在导出阶段与下载阶段之间对齐同一条目。
type ExportFile struct {
	Key  string
	Name string
	Size int64
}

// exportJSON tdl chat export 的输出结构（顶层为对象：{id, messages}）。
type exportJSON struct {
	ID       int64       `json:"id"`
	Messages []exportMsg `json:"messages"`
}

type exportMsg struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	File string          `json:"file"`
	Raw  json.RawMessage `json:"raw"`
}

// rawMessage 对应导出文件里 message.raw（gotd Message 结构序列化，键为 Go 导出字段名）。
type rawMessage struct {
	Media *rawMedia `json:"Media"`
}

// rawMedia 只关心尺寸相关的字段。
type rawMedia struct {
	Document *struct {
		Size     int64  `json:"Size"`
		MimeType string `json:"MimeType"`
	} `json:"Document"`
	Photo *struct {
		Sizes []json.RawMessage `json:"Sizes"`
	} `json:"Photo"`
}

// ParseExportFile 解析 tdl chat export 的 JSON，返回媒体文件列表（名称 + 大小）。
// 无媒体字段或大小未知的条目 size 记 0。
func ParseExportFile(path string) ([]ExportFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var data exportJSON
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&data); err != nil {
		return nil, fmt.Errorf("parse export json: %w", err)
	}
	out := make([]ExportFile, 0, len(data.Messages))
	for _, m := range data.Messages {
		if strings.TrimSpace(m.File) == "" {
			continue
		}
		ef := ExportFile{Name: m.File}
		if data.ID != 0 && m.ID != 0 {
			ef.Key = fmt.Sprintf("%d_%d", data.ID, m.ID)
			// 磁盘命名遵循 tdl 默认模板 {对话ID}_{消息ID}_{文件名}
			ef.Name = ef.Key + "_" + m.File
		}
		if len(m.Raw) > 0 {
			var rm rawMessage
			if json.Unmarshal(m.Raw, &rm) == nil && rm.Media != nil {
				switch {
				case rm.Media.Document != nil:
					ef.Size = rm.Media.Document.Size
				case rm.Media.Photo != nil:
					ef.Size = photoRawSize(rm.Media.Photo.Sizes)
				}
			}
		}
		out = append(out, ef)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("export file has no media messages")
	}
	return out, nil
}

// photoRawSize 从 photo 的 Sizes 数组里取最大可下载尺寸估算值。
// 元素可能是 PhotoSize{W,H,Size}、PhotoSizeProgressive{Sizes:[...]} 等。
func photoRawSize(elems []json.RawMessage) int64 {
	var best int64
	for _, el := range elems {
		var ps struct {
			Size  int64   `json:"Size"`
			Sizes []int64 `json:"Sizes"`
		}
		if json.Unmarshal(el, &ps) != nil {
			continue
		}
		if len(ps.Sizes) > 0 {
			last := ps.Sizes[len(ps.Sizes)-1]
			if last > best {
				best = last
			}
		}
		if ps.Size > best {
			best = ps.Size
		}
	}
	return best
}
