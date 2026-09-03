package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// GET /api/fs/dirs?path=/vol1
// 列出指定目录下的子目录，供前端目录选择器使用。
// 仅返回目录（忽略隐藏目录），不做写操作，路径必须是绝对路径。

type fsDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		// 默认根：优先 /vol1（飞牛存储池），不存在则用 /
		if _, err := os.Stat("/vol1"); err == nil {
			path = "/vol1"
		} else {
			path = "/"
		}
	}
	if !strings.HasPrefix(path, "/") {
		fail(w, 400, "路径必须是绝对路径")
		return
	}
	path = filepath.Clean(path)
	if path == "." || path == ".." {
		fail(w, 400, "非法路径")
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		fail(w, 400, "无法读取目录: "+err.Error())
		return
	}

	dirs := make([]fsDirEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, fsDirEntry{Name: e.Name(), Path: filepath.Join(path, e.Name())})
		}
	}

	parent := ""
	if path != "/" {
		parent = filepath.Dir(path)
	}

	ok(w, map[string]any{
		"path":   path,
		"parent": parent,
		"dirs":   dirs,
	})
}
