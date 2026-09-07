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
	appID, appHash, _ := cfg.AppCredential()
	return tdl.ClientConfig{
		DataDir:   s.cfg.TdlDataDir,
		Namespace: s.cfg.Namespace,
		Proxy:     cfg.Proxy,
		AppID:     appID,
		AppHash:   appHash,
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
	s.me.save(info) // 登录成功即写入缓存，避免下次 auth/me 再起客户端实测
	ok(w, info)
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	// 退出登录要删 bolt 里的 session 键，下载占锁期间会阻塞
	if s.tasks.ServeBusy() {
		fail(w, codeBizErr, "有任务正在下载，请先暂停或等待完成后再退出登录")
		return
	}
	err := tdl.ClearSession(rctx(5*time.Second), s.cfg.TdlDataDir, s.cfg.Namespace)
	if err != nil {
		fail(w, codeBizErr, "退出登录失败: "+err.Error())
		return
	}
	s.me.clear()
	ok(w, true)
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	// 下载占锁期间（bolt 被 serve 独占）实测会阻塞到下载结束，
	// 直接回缓存；无缓存的极端情况回落实测（有 20s 超时兜底）。
	if s.tasks.ServeBusy() {
		if info := s.me.load(); info != nil {
			ok(w, info)
			return
		}
	}

	// 不占锁时照旧实测：会话被远端吊销、换号等靠这里纠偏并刷新缓存
	ctx := rctx(20 * time.Second)
	cfg := s.clientConfig()

	has, err := tdl.HasSession(ctx, cfg.DataDir, cfg.Namespace)
	if err == nil && !has {
		s.me.clear()
		fail(w, codeBizErr, "not logged in")
		return
	}
	info, err := tdl.Me(ctx, cfg)
	if err != nil {
		fail(w, codeBizErr, "not logged in")
		return
	}
	s.me.save(info)
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

	// 正有任务下载时 tdl bolt 存储锁被占，同步探测会阻塞到下载结束。
	// 立即返回提示，用户可直接创建任务（自动排队，启动后补探测）。
	if s.tasks.ServeBusy() {
		results := make([]tdl.ProbeResult, len(urls))
		tip := "有任务正在下载，暂时无法解析预览；可直接点击开始下载，新任务会自动排队"
		for i, u := range urls {
			results[i] = tdl.ProbeResult{URL: u, Error: tip}
		}
		ok(w, results)
		return
	}

	ctx := rctx(60 * time.Second)
	results := tdl.Probe(ctx, s.clientConfig(), urls)
	ok(w, results)
}

// createSelectionReq 前端在选择资源后提交的勾选范围。
type createSelectionReq struct {
	// PeerID 由资源清单接口返回，用于生成 tdl -f 清单
	PeerID int64 `json:"peerId"`
	MsgIDs []int `json:"msgIds"`
}

type createReq struct {
	URLs        []string                      `json:"urls"`
	Group       bool                          `json:"group"`
	SavePath    string                        `json:"savePath"`
	Include     string                        `json:"include"`
	Exclude     string                        `json:"exclude"`
	Concurrency int                           `json:"concurrency"`
	Selected    map[string]createSelectionReq `json:"selected"`
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

// handleURLLookup 批量查询链接台账：POST {urls:[...]} → [{url,status,name,finishedAt}]。
// 前端解析链接后调用，提示"已下载过/下载中"等，防止重复操作。
func (s *Server) handleURLLookup(w http.ResponseWriter, r *http.Request) {
	req, err := body[struct {
		URLs []string `json:"urls"`
	}](r)
	if err != nil {
		fail(w, codeBizErr, "参数错误")
		return
	}
	urls := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	entries := s.tasks.LookupURLs(urls)
	out := make([]task.URLEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	ok(w, out)
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

	// 创建前先探测一次，填充任务名 / 类型 / 大小（失败不阻塞创建）。
	// 正有任务下载时 tdl bolt 存储锁被占，同步探测会阻塞到下载结束
	// （表现为前端一直转圈）——此时跳过探测，任务先入队（名称/大小
	// 以链接占位），任务实际启动拿到锁后再补探测回填。
	ctx := rctx(60 * time.Second)
	urls := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	probeMap := map[string]tdl.ProbeResult{}
	if len(urls) > 0 && !s.tasks.ServeBusy() {
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

	// 选择性下载：按 URL 组装勾选范围，文件名 / 大小优先从资源清单缓存回填
	selection := map[string]task.Selection{}
	for u, sel := range req.Selected {
		u = strings.TrimSpace(u)
		if u == "" || len(sel.MsgIDs) == 0 {
			continue
		}
		selection[u] = task.Selection{
			PeerID: sel.PeerID,
			MsgIDs: sel.MsgIDs,
			Files:  s.resCache.selectionFiles(u, sel.MsgIDs),
		}
	}

	tasks, err := s.tasks.Create(task.CreateInput{
		URLs:        valid,
		Group:       req.Group,
		SavePath:    req.SavePath,
		Include:     splitCSV(req.Include),
		Exclude:     splitCSV(req.Exclude),
		ProbeResult: probeMap,
		Selection:   selection,
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
