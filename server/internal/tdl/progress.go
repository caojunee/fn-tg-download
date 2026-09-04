package tdl

import (
	"regexp"
	"strconv"
	"strings"
)

// tdl 的进度条由 go-pretty progress 渲染到 stdout，每 100ms 一帧，
// 帧内每个 tracker 一行，形如（SIT 实测 0.20.x，非 TTY 下以 \n + \r 混合分隔）：
//
//	CPU: 0.00% Memory: 35.70 MB Goroutines: 22              ← 系统统计行（无标记，忽略）
//	Telegram News(1005640892):193~  ...  50.5% [####....] [1.00 MB in 7s; 139 KB/s]
//	[####.....................] [7s; 139.40 KB/s]           ← 进度条行（无标记，忽略）
//
// tracker 行的显示名是「{聊天标题}({对话ID}):{消息ID}~」，不含文件名；
// 磁盘文件名遵循模板 {对话ID}_{消息ID}_{文件名}，二者通过 ID 关联。
var (
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// trackerRe 从 tracker 行提取 (对话ID):(消息ID)~ 标记
	trackerRe = regexp.MustCompile(`\((\d+)\):(\d+)~`)
	percentRe = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%`)
	speedRe   = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([KMGT]?i?B)/s`)
)

// ProgressSample 从一行进度输出中提取的信息。
type ProgressSample struct {
	Percent  float64 // 0-100（done! 视为 100）
	Speed    float64 // 字节/秒
	DialogID string // tracker 对应的对话 ID
	MsgID    string // tracker 对应的消息 ID
}

// ParseProgressLine 解析一行 tdl 进度输出。
// 仅 tracker 行（含 (对话ID):(消息ID)~ 标记）返回 ok=true，
// CPU/内存统计行、进度条行、普通日志行均返回 ok=false。
func ParseProgressLine(raw string) (ProgressSample, bool) {
	line := ansiRe.ReplaceAllString(raw, "")
	if strings.TrimSpace(line) == "" {
		return ProgressSample{}, false
	}

	m := trackerRe.FindStringSubmatchIndex(line)
	if m == nil {
		return ProgressSample{}, false
	}
	s := ProgressSample{
		DialogID: line[m[2]:m[3]],
		MsgID:    line[m[4]:m[5]],
	}
	rest := line[m[1]:] // 标记之后的部分，百分比/速率只在其中找，避免标题数字误匹配

	if strings.Contains(rest, "done!") {
		s.Percent = 100
	} else if pm := percentRe.FindStringSubmatch(rest); pm != nil {
		p, err := strconv.ParseFloat(pm[1], 64)
		if err != nil || p < 0 || p > 100 {
			return ProgressSample{}, false
		}
		s.Percent = p
	} else {
		return ProgressSample{}, false
	}

	if sm := speedRe.FindStringSubmatch(rest); sm != nil {
		v, err := strconv.ParseFloat(sm[1], 64)
		if err == nil {
			s.Speed = v * unitMultiplier(sm[2])
		}
	}
	return s, true
}

func unitMultiplier(unit string) float64 {
	unit = strings.TrimSuffix(unit, "B")
	unit = strings.TrimSuffix(unit, "i")
	switch strings.ToUpper(unit) {
	case "K":
		return 1024
	case "M":
		return 1024 * 1024
	case "G":
		return 1024 * 1024 * 1024
	case "T":
		return 1024 * 1024 * 1024 * 1024
	default:
		return 1
	}
}
