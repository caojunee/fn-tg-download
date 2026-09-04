package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tg-download/internal/settings"
	"tg-download/internal/task"
	"tg-download/internal/tdl"
)

// clientConfig 从当前设置组装 Telegram 客户端配置。
func (s *Server) clientConfig() tdl.ClientConfig {
	cfg := s.settings.Get()
	return tdl.ClientConfig{
		DataDir:   s.cfg.TdlDataDir,
		Namespace: s.cfg.Namespace,
		Proxy:     cfg.Proxy,
	}
}

// ---- 认证 ----

type sendCodeReq struct {
	Phone string `json:"phone"`
}

func (s *Server) handleSendCode(w http.ResponseWriter, r *http.Request) {
	req, err := body[sendCodeReq](r)
	if err != nil || strings.TrimSpace(req.Phone) == "" {
		fail(w, codeBizErr, "手机号不能为空")
		return
	}
	phone := normalizePhone(req.Phone)

	// 单用户：已有进行中的流程则先关闭
	s.loginMu.Lock()
	if s.flow != nil {
		s.flow.Close()
	}
	s.flow = tdl.NewFlow(s.clientConfig(), phone)
	s.loginMu.Unlock()

	ok(w, map[string]any{"sent": true, "timeout": 120})
}

type loginReq struct {
	Phone    string `json:"phone"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	req, err := body[loginReq](r)
	if err != nil || strings.TrimSpace(req.Code) == "" {
		fail(w, codeBizErr, "验证码不能为空")
		return
	}

	s.loginMu.Lock()
	flow := s.flow
	s.loginMu.Unlock()
	if flow == nil {
		fail(w, codeBizErr, "请先获取验证码")
		return
	}

	var info *tdl.UserInfo
	if req.Password != "" {
		info, err = flow.SubmitPassword(req.Password)
	} else {
		info, err = flow.SubmitCode(strings.TrimSpace(req.Code), "")
	}
	if err != nil {
		if errors.Is(err, tdl.ErrPasswordNeeded) {
			fail(w, codeNeedPwd, "账号已开启两步验证，请输入密码")
			return
		}
		fail(w, codeBizErr, "登录失败: "+err.Error())
		return
	}

	s.loginMu.Lock()
	s.flow = nil
	s.loginMu.Unlock()
	ok(w, info)
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	if err := tdl.ClearSession(rctx(5*time.Second), s.cfg.TdlDataDir, s.cfg.Namespace); err != nil {
		fail(w, codeBizErr, "退出登录失败: "+err.Error())
		return
	}
	ok(w, true)
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	ctx := rctx(20 * time.Second)
	cfg := s.clientConfig()

	has, err := tdl.HasSession(ctx, cfg.DataDir, cfg.Namespace)
	if err == nil && !has {
		fail(w, codeBizErr, "not logged in")
		return
	}
	info, err := tdl.Me(ctx, cfg)
	if err != nil {
		fail(w, codeBizErr, "not logged in")
		return
	}
	ok(w, info)
}

func normalizePhone(p string) string {
	p = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')':
			return -1
		}
		return r
	}, strings.TrimSpace(p))
	if !strings.HasPrefix(p, "+") {
		p = "+" + p
	}
	return p
}

func rctx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	// 独立的短生命周期请求上下文，无需主动 cancel（到时自释放）
	_ = cancel
	return ctx
}

// ---- 下载 ----

type probeReq struct {
	URLs []string `json:"urls"`
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	req, err := body[probeReq](r)
	if err != nil || len(req.URLs) == 0 {
		fail(w, codeBizErr, "链接列表不能为空")
		return
	}
	urls := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		fail(w, codeBizErr, "链接列表不能为空")
		return
	}

	ctx := rctx(60 * time.Second)
	results := tdl.Probe(ctx, s.clientConfig(), urls)
	ok(w, results)
}

type createReq struct {
	URLs        []string `json:"urls"`
	Group       bool     `json:"group"`
	SavePath    string   `json:"savePath"`
	Include     string   `json:"include"`
	Exclude     string   `json:"exclude"`
	Concurrency int      `json:"concurrency"`
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.TrimPrefix(strings.ToLower(p), "."))
		}
	}
	return out
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	req, err := body[createReq](r)
	if err != nil {
		fail(w, codeBizErr, "参数错误")
		return
	}
	if len(req.URLs) == 0 {
		fail(w, codeBizErr, "链接不能为空")
		return
	}

	// 创建前先探测一次，填充任务名 / 类型 / 大小（失败不阻塞创建）
	ctx := rctx(60 * time.Second)
	urls := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	probeMap := map[string]tdl.ProbeResult{}
	if len(urls) > 0 {
		for _, pr := range tdl.Probe(ctx, s.clientConfig(), urls) {
			probeMap[pr.URL] = pr
		}
	}
	// 拦截探测失败且非私有链接的 URL：纯文本消息（tdl 空跑）、消息
	// 不存在、链接不支持等。私有链接探测失败属预期，放行由 tdl 兜底。
	valid := make([]string, 0, len(urls))
	reasons := make([]string, 0)
	for _, u := range urls {
		if pr := probeMap[u]; pr.Error != "" && !tdl.IsPrivateLink(u) {
			reasons = append(reasons, u+"：探测失败，已跳过（"+pr.Error+"）")
			continue
		}
		valid = append(valid, u)
	}
	if len(valid) == 0 {
		fail(w, codeBizErr, "没有可下载的链接\n"+strings.Join(reasons, "\n"))
		return
	}
	// 响应头必须在写响应体之前设置（WriteHeader 之后再 Set 会静默失效）
	if len(reasons) > 0 {
		w.Header().Set("X-Skipped-Reasons", strings.Join(reasons, "\n"))
	}

	tasks, err := s.tasks.Create(task.CreateInput{
		URLs:        valid,
		Group:       req.Group,
		SavePath:    req.SavePath,
		Include:     splitCSV(req.Include),
		Exclude:     splitCSV(req.Exclude),
		ProbeResult: probeMap,
	})
	if err != nil {
		fail(w, codeBizErr, "创建任务失败: "+err.Error())
		return
	}
	ok(w, tasks)
}

func (s *Server) handleListTasks(w http.ResponseWriter, _ *http.Request) {
	ok(w, s.tasks.List())
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	req, err := body[struct {
		IDs []string `json:"ids"`
	}](r)
	if err != nil || len(req.IDs) == 0 {
		err = s.tasks.ClearHistory()
	} else {
		err = s.tasks.Remove(req.IDs)
	}
	if err != nil {
		fail(w, codeBizErr, err.Error())
		return
	}
	ok(w, true)
}

type actionFunc func(id string, deletePartial bool) error

func (s *Server) taskAction(fnName string) http.HandlerFunc {
	var fn actionFunc
	switch fnName {
	case "pause":
		fn = func(id string, _ bool) error { return s.tasks.Pause(id) }
	case "resume":
		fn = func(id string, _ bool) error { return s.tasks.Resume(id) }
	case "cancel":
		fn = s.tasks.Cancel
	case "retry":
		fn = func(id string, _ bool) error { return s.tasks.Retry(id) }
	default:
		fn = func(string, bool) error { return errors.New("unknown action") }
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// 可选 body：{"deletePartial":true} 仅取消动作使用（删除已下载分片）
		deletePartial := false
		if r.Body != nil {
			var body struct {
				DeletePartial bool `json:"deletePartial"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				deletePartial = body.DeletePartial
			}
		}
		if err := fn(id, deletePartial); err != nil {
			fail(w, codeBizErr, err.Error())
			return
		}
		ok(w, true)
	}
}

// ---- 设置 ----

func (s *Server) toFrontend() map[string]any {
	return map[string]any{
		"downloadPath": s.settings.Get().DownloadPath,
		"proxy":        s.settings.Get().Proxy,
		"concurrency":  s.settings.Get().Concurrency,
		"autoGroup":    s.settings.Get().AutoGroup,
		"maxSizeMB":    s.settings.Get().MaxSizeMB,
		"speedLimitKB": s.settings.Get().SpeedLimitKB,
		"tdlPath":      s.settings.Get().TdlPath,
		"tdlDataDir":   s.cfg.TdlDataDir,
		"version":      Version,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	ok(w, s.toFrontend())
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	req, err := body[settings.Settings](r)
	if err != nil {
		fail(w, codeBizErr, "参数错误")
		return
	}
	if _, err := s.settings.Save(req); err != nil {
		fail(w, codeBizErr, "保存设置失败: "+err.Error())
		return
	}
	ok(w, s.toFrontend())
}

func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	req, err := body[struct {
		Proxy string `json:"proxy"`
	}](r)
	if err != nil {
		fail(w, codeBizErr, "参数错误")
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	if p := strings.TrimSpace(req.Proxy); p != "" {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if u, err := url.Parse(p); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
		client.Transport = transport
	}

	start := time.Now()
	resp, err := client.Get("https://www.google.com/generate_204")
	if err != nil {
		ok(w, map[string]any{"ok": false, "latency": 0})
		return
	}
	_ = resp.Body.Close()
	ok(w, map[string]any{"ok": true, "latency": time.Since(start).Milliseconds()})
}
