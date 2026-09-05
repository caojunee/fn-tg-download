package tdl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
)

// tdl 内置的应用身份：code 登录使用 tdesktop 的 app（与 tdl login -T code 完全一致，
// 保证本模块写入的 session 与 tdl CLI 共用无冲突）。
var (
	AppID      = 2040
	AppHash    = "b18441a1ff607e10a989891a5462e627"
	deviceInfo = telegram.DeviceConfig{
		DeviceModel:    "Desktop",
		SystemVersion:  "Windows 10",
		AppVersion:     "4.2.4 x64",
		LangCode:       "en",
		SystemLangCode: "en-US",
		LangPack:       "tdesktop",
	}
)

var (
	ErrPasswordNeeded = errors.New("password required")
	ErrFlowClosed     = errors.New("login flow closed")
	ErrCodeTimeout    = errors.New("code timeout")
)

// UserInfo 与前端 UserInfo 结构对齐。
type UserInfo struct {
	ID        int64  `json:"id"`
	Phone     string `json:"phone"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName,omitempty"`
	Username  string `json:"username,omitempty"`
	Initial   string `json:"initial"`
}

// ClientConfig 描述连接 Telegram 所需的环境。
type ClientConfig struct {
	DataDir   string // tdl bolt 目录
	Namespace string
	Proxy     string
	// AppID / AppHash 为可选的应用身份；为空（AppID<=0 或 AppHash 为空）
	// 时回落到内置的 tdesktop 身份（与 tdl CLI 一致，保证 session 共用）。
	AppID   int
	AppHash string
}

// sessionBridge 把 BoltKV 适配为 gotd 的 telegram.SessionStorage。
// login=true 时 LoadSession 返回空（登录流程不加载旧 session，与 tdl 行为一致）。
type sessionBridge struct {
	kv      *BoltKV
	isLogin bool
}

func (s *sessionBridge) LoadSession(ctx context.Context) ([]byte, error) {
	if s.isLogin {
		return nil, nil
	}
	v, err := s.kv.Get(ctx, keySession)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return v, err
}

func (s *sessionBridge) StoreSession(ctx context.Context, data []byte) error {
	return s.kv.Set(ctx, keySession, data)
}

// newClient 用指定配置创建 gotd 客户端（复用 tdl 的存储与身份）。
func newClient(ctx context.Context, cfg ClientConfig, isLogin bool) (*telegram.Client, *BoltKV, error) {
	kv, err := OpenBolt(cfg.DataDir, cfg.Namespace)
	if err != nil {
		return nil, nil, err
	}
	dialer, err := dialerFromProxy(cfg.Proxy)
	if err != nil {
		_ = kv.Close()
		return nil, nil, err
	}
	appID, appHash := AppID, AppHash
	if cfg.AppID > 0 && strings.TrimSpace(cfg.AppHash) != "" {
		appID, appHash = cfg.AppID, strings.TrimSpace(cfg.AppHash)
	}
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: &sessionBridge{kv: kv, isLogin: isLogin},
		Device:         deviceInfo,
		Resolver:       resolverWithDialer(dialer),
		DialTimeout:    15 * time.Second,
		MaxRetries:     3,
		RetryInterval:  3 * time.Second,
	})
	return client, kv, nil
}

// dialerFromProxy 将代理字符串转换为拨号器，支持 http(s) CONNECT 与 socks5。
func dialerFromProxy(p string) (proxy.ContextDialer, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return &directDialer{}, nil
	}
	u, err := url.Parse(p)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy: %w", err)
	}
	switch u.Scheme {
	case "socks5", "socks5h":
		d, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, err
		}
		if cd, ok := d.(proxy.ContextDialer); ok {
			return cd, nil
		}
		return &contextDialerAdapter{d: d}, nil
	case "http", "https", "":
		return &httpConnectDialer{proxyAddr: u.Host}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
	}
}

type directDialer struct{}

func (d *directDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var nd net.Dialer
	return nd.DialContext(ctx, network, addr)
}

type contextDialerAdapter struct {
	d proxy.Dialer
}

func (a *contextDialerAdapter) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if cd, ok := a.d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	return nil, errors.New("dialer does not support context dialing")
}

// httpConnectDialer 通过 HTTP CONNECT 隧道拨号（MTProto 是原始 TCP，不能用 http.Transport）。
type httpConnectDialer struct {
	proxyAddr string
}

func (h *httpConnectDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", h.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	_ = conn.SetDeadline(deadline)

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	if _, err = conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := readResponse(conn, buf)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	head := string(buf[:n])
	if !strings.Contains(head, " 200 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", strings.TrimSpace(firstLine(head)))
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func readResponse(conn net.Conn, buf []byte) (int, error) {
	total := 0
	tmp := make([]byte, 1)
	for total < len(buf) {
		n, err := conn.Read(tmp)
		if err != nil {
			return total, err
		}
		buf[total] = tmp[0]
		total += n
		if total >= 4 && string(buf[total-4:total]) == "\r\n\r\n" {
			return total, nil
		}
	}
	return total, nil
}

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// resolverWithDialer 基于 dialer 构造 gotd 的 MTProto 解析器（明文 DC 连接走自定义拨号）。
func resolverWithDialer(d proxy.ContextDialer) dcs.Resolver {
	return dcs.Plain(dcs.PlainOptions{Dial: d.DialContext})
}

// Flow 管理一次完整的验证码登录流程（phone → code → 可选 2FA password）。
// 与 tdl login 一样：全程保持同一连接，验证码发送与提交共用上下文。
type Flow struct {
	cfg   ClientConfig
	phone string

	ctx    context.Context
	cancel context.CancelFunc

	codeCh   chan string
	pwdCh    chan string
	needPwd  chan struct{}
	needOnce sync.Once
	userCh   chan *UserInfo
	errCh    chan error
	finished chan struct{}

	closeOnce sync.Once
}

// NewFlow 创建登录流程并立即启动连接。
func NewFlow(cfg ClientConfig, phone string) *Flow {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Flow{
		cfg:      cfg,
		phone:    phone,
		ctx:      ctx,
		cancel:   cancel,
		codeCh:   make(chan string),
		pwdCh:    make(chan string),
		needPwd:  make(chan struct{}, 1),
		userCh:   make(chan *UserInfo, 1),
		errCh:    make(chan error, 1),
		finished: make(chan struct{}),
	}
	go f.run()
	return f
}

func (f *Flow) run() {
	defer close(f.finished)
	client, kv, err := newClient(f.ctx, f.cfg, true)
	if err != nil {
		f.errCh <- err
		return
	}
	defer kv.Close()

	err = client.Run(f.ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(&termAuth{f: f}, auth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}
		user, err := client.Self(ctx)
		if err != nil {
			return err
		}
		f.userCh <- toUserInfo(user)
		return nil
	})
	if err != nil {
		select {
		case f.errCh <- err:
		default:
		}
	}
}

// termAuth 实现 gotd 的 auth.UserAuthenticator，把交互桥接到 Flow 的 channel。
type termAuth struct {
	f *Flow
}

func (a *termAuth) Phone(_ context.Context) (string, error) { return a.f.phone, nil }

func (a *termAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	select {
	case code := <-a.f.codeCh:
		return code, nil
	case <-a.f.ctx.Done():
		return "", ErrFlowClosed
	case <-time.After(5 * time.Minute):
		return "", ErrCodeTimeout
	}
}

func (a *termAuth) Password(_ context.Context) (string, error) {
	// 通知上层：需要两步验证密码（若上层未提供，将保持等待直到超时）
	a.f.needOnce.Do(func() { a.f.needPwd <- struct{}{} })
	select {
	case p := <-a.f.pwdCh:
		return p, nil
	case <-a.f.ctx.Done():
		return "", ErrFlowClosed
	case <-time.After(3 * time.Minute):
		return "", errors.New("password timeout")
	}
}

func (a *termAuth) AcceptTermsOfService(_ context.Context, _ tg.HelpTermsOfService) error {
	return nil
}

func (a *termAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign up is not supported")
}

// SubmitCode 提交验证码（可选附带两步验证密码），阻塞直到登录结果。
// 若服务端要求两步验证而 password 为空，返回 ErrPasswordNeeded，
// 此时流程保持存活，可继续调用 SubmitPassword。
func (f *Flow) SubmitCode(code, password string) (*UserInfo, error) {
	select {
	case f.codeCh <- code:
	case <-f.ctx.Done():
		return nil, ErrFlowClosed
	}
	return f.wait(password)
}

// SubmitPassword 提交两步验证密码。
func (f *Flow) SubmitPassword(password string) (*UserInfo, error) {
	select {
	case f.pwdCh <- password:
	case <-f.ctx.Done():
		return nil, ErrFlowClosed
	}
	return f.wait("")
}

func (f *Flow) wait(password string) (*UserInfo, error) {
	for {
		select {
		case u := <-f.userCh:
			f.Close()
			return u, nil
		case err := <-f.errCh:
			f.Close()
			return nil, err
		case <-f.needPwd:
			if password != "" {
				select {
				case f.pwdCh <- password:
					password = "" // 只发一次
					continue
				case <-f.ctx.Done():
					return nil, ErrFlowClosed
				}
			}
			// 流程保持存活等待 SubmitPassword
			return nil, ErrPasswordNeeded
		case <-time.After(6 * time.Minute):
			f.Close()
			return nil, ErrCodeTimeout
		}
	}
}

// Close 终止登录流程。
func (f *Flow) Close() {
	f.closeOnce.Do(func() {
		f.cancel()
		<-f.finished
	})
}

// Me 返回当前登录账号信息（需要 session 有效）。
func Me(ctx context.Context, cfg ClientConfig) (*UserInfo, error) {
	client, kv, err := newClient(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	defer kv.Close()

	var info *UserInfo
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			return errors.New("not authorized")
		}
		user, err := client.Self(ctx)
		if err != nil {
			return err
		}
		info = toUserInfo(user)
		return nil
	})
	return info, err
}

func toUserInfo(u *tg.User) *UserInfo {
	info := &UserInfo{
		ID:        u.ID,
		Phone:     "+" + strings.TrimPrefix(u.Phone, "+"),
		FirstName: u.FirstName,
		LastName:  u.LastName,
		Username:  u.Username,
	}
	name := info.FirstName
	if name == "" {
		name = info.Username
	}
	if name == "" {
		name = "?"
	}
	info.Initial = strings.ToUpper(string([]rune(name)[:1]))
	return info
}
