package tdl

import "testing"

func TestSanitizeDirName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"my love", "my love"},
		{"test", "test"},
		{`my/love:*?"<>|`, "my love"},
		{"  频道 4434014100  ", "频道 4434014100"},
		{"bad..name.", "bad..name"}, // 中间的点保留，仅去结尾
		{"...", ""},
		{"", ""},
		{"   ", ""},
		{"..", ""},
		{"a\\b", "a b"},
		{"con\t trol\x01", "con trol"},
	}
	for _, c := range cases {
		if got := SanitizeDirName(c.in); got != c.want {
			t.Errorf("SanitizeDirName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDirNameForURL(t *testing.T) {
	cases := []struct {
		url, title, want string
	}{
		// 标题优先
		{"https://t.me/mylove4877/896", "my love", "my love"},
		// 公开频道无标题 → username
		{"https://t.me/baisi_cos/123", "", "baisi_cos"},
		// 私有频道无标题 → 频道 <id>
		{"https://t.me/c/4434014100/2", "", "频道 4434014100"},
		// 标题含非法字符 → sanitize
		{"https://t.me/x/1", `a/b:c`, "a b c"},
		// 非 t.me 链接不分子目录
		{"https://example.com/file.zip", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := DirNameForURL(c.url, c.title); got != c.want {
			t.Errorf("DirNameForURL(%q,%q) = %q, want %q", c.url, c.title, got, c.want)
		}
	}
}
