// Package api 提供 HTTP API 与静态文件托管。
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tg-download/internal/config"
	"tg-download/internal/settings"
	"tg-download/internal/task"
	"tg-download/internal/tdl"
)

// Version 由 main 通过 ldflags 注入（-X tg-download/internal/api.Version=x.y.z）。
var Version = "dev"

// Server HTTP 服务。
type Server struct {
	cfg      config.Config
	settings *settings.Store
	tasks    *task.Manager

	// 登录流程：单用户场景，同一时刻最多一个进行中的验证码登录
	loginMu sync.Mutex
	flow    *tdl.Flow

	// 资源清单缓存：url → 最近一次枚举结果，用于创建任务时
	// 回填选中项的文件名与大小（枚举成本高，不重复拉取）
	resCache *resourceCache

	// 登录信息缓存：下载占锁期间 /api/auth/me 直接回缓存（见 me.go）
	me *meCache
}

// New 创建 Server。
func New(cfg config.Config, st *settings.Store, tm *task.Manager) *Server {
	return &Server{cfg: cfg, settings: st, tasks: tm,
		resCache: newResourceCache(), me: newMeCache(cfg.DataDir)}
}

// ---- 统一响应 {code, message, data} ----

type resp struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func ok(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, resp{Code: 0, Message: "ok", Data: data})
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, http.StatusOK, resp{Code: code, Message: msg})
}

func writeJSON(w http.ResponseWriter, status int, body resp) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// code 约定：0 成功；1 业务错误；2 需要两步验证密码
const (
	codeOK        = 0
	codeBizErr    = 1
	codeNeedPwd   = 2
)

// Register 注册所有路由。
func (s *Server) Register(mux *http.ServeMux) {
	// 认证
	mux.HandleFunc("POST /api/auth/send-code", s.handleSendCode)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)

	// 下载
	mux.HandleFunc("POST /api/download/probe", s.handleProbe)
	mux.HandleFunc("POST /api/download/urls/lookup", s.handleURLLookup)
	mux.HandleFunc("POST /api/download/create", s.handleCreate)
	mux.HandleFunc("GET /api/download/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/download/history/clear", s.handleClearHistory)
	mux.HandleFunc("POST /api/download/tasks/{id}/pause", s.taskAction("pause"))
	mux.HandleFunc("POST /api/download/tasks/{id}/resume", s.taskAction("resume"))
	mux.HandleFunc("POST /api/download/tasks/{id}/cancel", s.taskAction("cancel"))
	mux.HandleFunc("POST /api/download/tasks/{id}/retry", s.taskAction("retry"))

	// 资源选择（整频道 / 相册：列出可选条目并按需取缩略图）
	mux.HandleFunc("POST /api/resource/list", s.handleListResources)
	mux.HandleFunc("GET /api/resource/thumb", s.handleResourceThumb)

	// 设置
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("POST /api/settings", s.handleSaveSettings)
	mux.HandleFunc("POST /api/settings/proxy/test", s.handleTestProxy)

	// 文件系统（目录选择器）
	mux.HandleFunc("GET /api/fs/dirs", s.handleListDirs)
}

// ServeStatic 托管前端构建产物（SPA fallback 到 index.html）。
func (s *Server) ServeStatic(mux *http.ServeMux) {
	if s.cfg.WebDir == "" {
		return
	}
	fs := http.FileServer(http.Dir(s.cfg.WebDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || !strings.Contains(path[1:], "/") {
			// 根路径或一级路径尝试静态文件，失败回退 index.html。
			// index.html 不缓存：入口引用带 hash 的 chunk，发版后旧 chunk
			// 会被删除，缓存旧入口会导致加载失败或看到旧版本。
			if path == "/" {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
				return
			}
			target := filepath.Join(s.cfg.WebDir, filepath.Clean("/"+path))
			if st, err := os.Stat(target); err == nil && !st.IsDir() {
				http.ServeFile(w, r, target)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
			return
		}
		// 带 hash 的静态资源长缓存（内容变化时 hash 必变）
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fs.ServeHTTP(w, r)
	})
}

// body 解析 JSON 请求体。
func body[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}
