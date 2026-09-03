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
}

// New 创建 Server。
func New(cfg config.Config, st *settings.Store, tm *task.Manager) *Server {
	return &Server{cfg: cfg, settings: st, tasks: tm}
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
	mux.HandleFunc("POST /api/download/create", s.handleCreate)
	mux.HandleFunc("GET /api/download/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/download/history/clear", s.handleClearHistory)
	mux.HandleFunc("POST /api/download/tasks/{id}/pause", s.taskAction("pause"))
	mux.HandleFunc("POST /api/download/tasks/{id}/resume", s.taskAction("resume"))
	mux.HandleFunc("POST /api/download/tasks/{id}/cancel", s.taskAction("cancel"))
	mux.HandleFunc("POST /api/download/tasks/{id}/retry", s.taskAction("retry"))

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
			// 根路径或一级路径尝试静态文件，失败回退 index.html
			target := filepath.Join(s.cfg.WebDir, filepath.Clean("/"+path))
			if path != "/" {
				if st, err := os.Stat(target); err == nil && !st.IsDir() {
					http.ServeFile(w, r, target)
					return
				}
			}
			http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// body 解析 JSON 请求体。
func body[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}
