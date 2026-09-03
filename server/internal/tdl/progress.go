package tdl

import (
	"regexp"
	"strconv"
	"strings"
)

// tdl 的进度条由 go-pretty progress 渲染到 stdout，每 100ms 一帧，
// 帧内每个 tracker 一行，形如（含 ANSI 色码，非 TTY 下仍有 \r 分隔）：
//
//	<文件名>  [tracker]  32.0%  [1.1 MB/s]  [00:12]  [00:30]
//
// 该解析器刻意写得宽容：仅提取百分比与速率，作为目录大小监控的兜底。
var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	percentRe   = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%`)
	speedRe     = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([KMGT]?i?B)/s`)
	overallHint = []string{"overall", "(total)", "total"}
)

// ProgressSample 从一行进度输出中提取的信息。
type ProgressSample struct {
	Percent float64 // 0-100
	Speed   float64 // 字节/秒
	Overall bool    // 是否为总览行
}

// ParseProgressLine 解析一行 tdl 进度输出，解析失败返回 ok=false。
func ParseProgressLine(raw string) (ProgressSample, bool) {
	line := ansiRe.ReplaceAllString(raw, "")
	if strings.TrimSpace(line) == "" {
		return ProgressSample{}, false
	}
	lower := strings.ToLower(line)
	s := ProgressSample{Overall: containsAny(lower, overallHint)}

	if m := percentRe.FindStringSubmatch(line); m != nil {
		p, err := strconv.ParseFloat(m[1], 64)
		if err != nil || p < 0 || p > 100 {
			return ProgressSample{}, false
		}
		s.Percent = p
	} else {
		return ProgressSample{}, false
	}

	if m := speedRe.FindStringSubmatch(line); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			s.Speed = v * unitMultiplier(m[2])
		}
	}
	return s, true
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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
