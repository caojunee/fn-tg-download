package tdl

import "testing"

// 用例取自 SIT 实测抓取的 tdl v0.20.4 原始 stdout（去 ANSI 前后对照）。
// 关键回归点：tracker 消息为「{标题}({对话ID}):{消息ID} -> {路径}」，
// 路径过长被 go-pretty 截断为 ~ 结尾；旧正则要求 ~ 紧跟消息 ID 导致永不匹配，
// 进而 filePerc 恒为空、取消任务无法删除分片（v0.3.4 bug）。
func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		ok       bool
		dialogID string
		msgID    string
		percent  float64
	}{
		{
			name:     "带目标路径的进行中行（当前实测格式）",
			raw:      "\x1b[34mtest(4434014100):2 -> /tmp/e2~\x1b[0m\x1b[34m ... \x1b[0m\x1b[91m 1.3%\x1b[0m\x1b[33m \x1b[33m[........]\x1b[0m\x1b[90m [\x1b[36m1.00 MB\x1b[0m\x1b[90m in \x1b[32m1s\x1b[0m\x1b[90m; \x1b[35m954.48 KB\x1b[0m\x1b[90m/s]\x1b[0m",
			ok:       true,
			dialogID: "4434014100",
			msgID:    "2",
			percent:  1.3,
		},
		{
			name:     "带目标路径的高进度行",
			raw:      "test(4434014100):3 -> /tmp/e2~ ...  88.8% [########] [60.00 MB in 20s; 3.11 MB/s]",
			ok:       true,
			dialogID: "4434014100",
			msgID:    "3",
			percent:  88.8,
		},
		{
			name:     "完成行 done!",
			raw:      "test(4434014100):2 -> /tmp/e2~ done!",
			ok:       true,
			dialogID: "4434014100",
			msgID:    "2",
			percent:  100,
		},
		{
			name:     "旧格式（无目标路径，~ 紧跟消息 ID）向后兼容",
			raw:      "Telegram News(1005640892):193~  ...  50.5% [####....] [1.00 MB in 7s; 139 KB/s]",
			ok:       true,
			dialogID: "1005640892",
			msgID:    "193",
			percent:  50.5,
		},
		{
			name: "CPU 统计行不匹配",
			raw:  "CPU: 0.00% Memory: 35.70 MB Goroutines: 22",
			ok:   false,
		},
		{
			name: "总进度条行不匹配",
			raw:  "[.................................................] [1s; 1.15 MB/s]",
			ok:   false,
		},
		{
			name: "目标路径含空格时仍能提取 ID",
			raw:  "test(4434014100):2 -> /vol1/1000/downloads/4434014100_2_League of Legends.mp4 ... 42.0% [....] [29.00 MB in 10s; 2.90 MB/s]",
			ok:   true,
			dialogID: "4434014100",
			msgID:    "2",
			percent:  42.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := ParseProgressLine(tc.raw)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (line: %q)", ok, tc.ok, ansiRe.ReplaceAllString(tc.raw, ""))
			}
			if !tc.ok {
				return
			}
			if s.DialogID != tc.dialogID || s.MsgID != tc.msgID {
				t.Fatalf("got %s:%s, want %s:%s", s.DialogID, s.MsgID, tc.dialogID, tc.msgID)
			}
			if s.Percent != tc.percent {
				t.Fatalf("percent = %v, want %v", s.Percent, tc.percent)
			}
		})
	}
}
