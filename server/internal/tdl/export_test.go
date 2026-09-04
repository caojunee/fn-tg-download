package tdl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelLink(t *testing.T) {
	cases := []struct {
		raw     string
		chat    string
		private bool
		ok      bool
	}{
		{"https://t.me/c/4434014100", "4434014100", true, true},
		{"https://t.me/c/4434014100/", "4434014100", true, true},
		{"t.me/c/4434014100?topic=123", "4434014100", true, true},
		{"https://t.me/baisi_cos", "baisi_cos", false, true},
		{"https://t.me/mylove4877/", "mylove4877", false, true},
		// 带消息 ID 的不是整频道链接
		{"https://t.me/c/4434014100/2", "", false, false},
		{"https://t.me/baisi_cos/123", "", false, false},
		{"https://t.me/c/4434014100/896?comment=1", "", false, false},
		{"https://example.com/foo", "", false, false},
	}
	for _, c := range cases {
		chat, private, ok := ChannelLink(c.raw)
		if ok != c.ok || chat != c.chat || private != c.private {
			t.Errorf("ChannelLink(%q) = (%q,%v,%v), want (%q,%v,%v)",
				c.raw, chat, private, ok, c.chat, c.private, c.ok)
		}
	}
}

// 用例取自 SIT 实测 `tdl chat export --with-content --raw` 的输出结构。
func TestParseExportFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.json")
	content := `{
	  "id": 4434014100,
	  "messages": [
	    {"id": 2, "type": "message", "date": 1788491000,
	     "file": "League of Legends (TM) Client 2026-03-23 13-16-14.mp4",
	     "raw": {"Media": {"Document": {"Size": 68618574, "MimeType": "video/mp4"}}}},
	    {"id": 3, "type": "message", "date": 1788492000,
	     "file": "a.jpg",
	     "raw": {"Media": {"Photo": {"Sizes": [{"W": 320, "H": 180, "Size": 16254}, {"Sizes": [1000, 2000, 45678]}, {"Type": "i"}]}}}},
	    {"id": 4, "type": "message", "date": 1788493000, "file": ""}
	  ]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ParseExportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2（无 file 的纯文本消息应跳过）", len(files))
	}
	if files[0].Key != "4434014100_2" {
		t.Errorf("Key = %q, want 4434014100_2", files[0].Key)
	}
	if files[0].Name != "4434014100_2_League of Legends (TM) Client 2026-03-23 13-16-14.mp4" {
		t.Errorf("Name = %q", files[0].Name)
	}
	if files[0].Size != 68618574 {
		t.Errorf("Size = %d, want 68618574", files[0].Size)
	}
	if files[1].Size != 45678 {
		t.Errorf("photo Size = %d, want 45678（progressive 取最后一档）", files[1].Size)
	}
}
