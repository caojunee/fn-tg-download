package tdl

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

// ProbeResult 对一条消息链接的解析结果，与前端 ProbeResult 结构对齐。
type ProbeResult struct {
	URL        string `json:"url"`
	ChatTitle  string `json:"chatTitle"`
	MessageID  int64  `json:"messageId"`
	Kind       string `json:"kind"` // single | album | group | channel
	FileCount  int    `json:"fileCount"`
	TotalSize  int64  `json:"totalSize"`
	Title      string `json:"title"`
	Error      string `json:"error,omitempty"`
}

// tmeRe 匹配 t.me 链接：
//
//	https://t.me/<username>/<msg>
//	https://t.me/<username>/<msg>/<end_msg>
//	https://t.me/c/<internal_id>/<msg>[/<end_msg>]
//	可选 ?comment=<id> / ?thread=<id>
var tmeRe = regexp.MustCompile(`^(?:https?://)?t\.me/(c/(\d+)|([A-Za-z0-9_]+))/(\d+)(?:/(\d+))?`)

type probeTarget struct {
	internalID int64 // 私有频道的内部 id（c/xxx）
	username   string
	msg        int64
	endMsg     int64
}

func parseTmeURL(raw string) (*probeTarget, error) {
	m := tmeRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return nil, fmt.Errorf("unsupported link format: %s", raw)
	}
	t := &probeTarget{msg: ato64(m[4])}
	if m[2] != "" {
		t.internalID = ato64(m[2])
	} else {
		t.username = m[3]
	}
	if m[5] != "" {
		t.endMsg = ato64(m[5])
	}
	return t, nil
}

func ato64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// Probe 解析一批链接，探测消息类型（单条/相册/组合消息）、文件数与总大小。
// 使用已登录的 session 建立连接。
func Probe(ctx context.Context, cfg ClientConfig, urls []string) []ProbeResult {
	results := make([]ProbeResult, len(urls))

	client, kv, err := newClient(ctx, cfg, false)
	if err != nil {
		for i, u := range urls {
			results[i] = ProbeResult{URL: u, Error: err.Error()}
		}
		return results
	}
	defer kv.Close()

	err = client.Run(ctx, func(ctx context.Context) error {
		api := tg.NewClient(client)
		for i, raw := range urls {
			results[i] = probeOne(ctx, api, raw)
		}
		return nil
	})
	if err != nil {
		for i, u := range urls {
			if results[i].Error == "" {
				results[i] = ProbeResult{URL: u, Error: err.Error()}
			}
		}
	}
	return results
}

func probeOne(ctx context.Context, api *tg.Client, raw string) ProbeResult {
	res := ProbeResult{URL: raw}

	// 整频道链接：只做轻量探测（拿标题），文件数/大小由任务启动后的
	// chat export 阶段填充，避免大频道阻塞创建请求。
	if chat, private, ok := ChannelLink(raw); ok {
		res.Kind = "channel"
		if private {
			res.ChatTitle = fmt.Sprintf("频道 %s", chat)
		} else {
			resolve, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: chat})
			if err == nil {
				if _, title := pickResolvedPeer(resolve); title != "" {
					res.ChatTitle = title
				}
			}
			if res.ChatTitle == "" {
				res.ChatTitle = chat
			}
		}
		res.Title = fmt.Sprintf("%s · 整频道", res.ChatTitle)
		return res
	}

	target, err := parseTmeURL(raw)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.MessageID = target.msg

	if target.internalID != 0 {
		// 私有频道（c/xxx）需要 access hash，只能从会话缓存取，
		// 探测阶段拿不到；下载阶段由 tdl 自身的 peers 缓存处理。
		res.Error = "private channel link cannot be probed without cached access hash; the download itself is still supported by tdl"
		return res
	}

	resolve, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: target.username})
	if err != nil {
		res.Error = fmt.Sprintf("failed to resolve channel: %v", err)
		return res
	}
	peer, chatTitle := pickResolvedPeer(resolve)
	if peer == nil {
		res.Error = "failed to resolve peer"
		return res
	}
	res.ChatTitle = chatTitle

	msgs, err := fetchMessages(ctx, api, peer, target.msg)
	if err != nil {
		res.Error = fmt.Sprintf("failed to fetch message: %v", err)
		return res
	}

	main := findMessage(msgs, target.msg)
	if main == nil {
		res.Error = "message not found (may have been deleted, or is not visible to the current account)"
		return res
	}

	// 相册 / 组合消息探测
	var mediaMsgs []*tg.Message
	if main.GroupedID != 0 {
		// 拉取附近消息，按 GroupedID 过滤
		from, to := target.msg-10, target.msg+10
		if from < 1 {
			from = 1
		}
		if target.endMsg > to {
			to = target.endMsg
		}
		nearby, err := fetchMessageRange(ctx, api, peer, from, to)
		if err == nil {
			for _, m := range nearby {
				if m.GroupedID != 0 && m.GroupedID == main.GroupedID && hasMedia(m) {
					mediaMsgs = append(mediaMsgs, m)
				}
			}
		}
		if len(mediaMsgs) == 0 {
			mediaMsgs = []*tg.Message{main}
		}
		res.Kind = "album"
	} else {
		mediaMsgs = []*tg.Message{main}
		res.Kind = "single"
	}

	for _, m := range mediaMsgs {
		res.TotalSize += mediaSize(m)
		res.FileCount++
	}

	if res.FileCount == 1 {
		res.Kind = "single"
	}
	res.Title = fmt.Sprintf("%s #%d", chatTitle, target.msg)
	if res.Kind == "album" {
		res.Title = fmt.Sprintf("%s #%d · Album (%d items)", chatTitle, target.msg, res.FileCount)
	}
	return res
}

func fetchMessages(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, id int64) ([]*tg.Message, error) {
	return fetchMessageRange(ctx, api, peer, id, id)
}

func fetchMessageRange(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, from, to int64) ([]*tg.Message, error) {
	ids := make([]tg.InputMessageClass, 0, to-from+1)
	for id := from; id <= to; id++ {
		ids = append(ids, &tg.InputMessageID{ID: int(id)})
	}

	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		req := &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
			ID:      ids,
		}
		resp, err := api.ChannelsGetMessages(ctx, req)
		if err != nil {
			return nil, err
		}
		return collectMessages(resp)
	case *tg.InputPeerUser:
		resp, err := api.MessagesGetMessages(ctx, ids)
		if err != nil {
			return nil, err
		}
		return collectMessages(resp)
	default:
		resp, err := api.MessagesGetMessages(ctx, ids)
		if err != nil {
			return nil, err
		}
		return collectMessages(resp)
	}
}

func collectMessages(box tg.MessagesMessagesClass) ([]*tg.Message, error) {
	switch v := box.(type) {
	case *tg.MessagesMessages:
		return filterMessages(v.Messages), nil
	case *tg.MessagesMessagesSlice:
		return filterMessages(v.Messages), nil
	case *tg.MessagesChannelMessages:
		return filterMessages(v.Messages), nil
	case *tg.MessagesMessagesNotModified:
		return nil, fmt.Errorf("messages not modified")
	default:
		return nil, fmt.Errorf("unexpected messages response type %T", box)
	}
}

func filterMessages(items []tg.MessageClass) []*tg.Message {
	out := make([]*tg.Message, 0, len(items))
	for _, it := range items {
		if m, ok := it.(*tg.Message); ok {
			out = append(out, m)
		}
	}
	return out
}

func findMessage(msgs []*tg.Message, id int64) *tg.Message {
	for _, m := range msgs {
		if m.ID == int(id) {
			return m
		}
	}
	return nil
}

func hasMedia(m *tg.Message) bool {
	return m.Media != nil
}

// mediaSize 提取消息媒体的可下载大小。
func mediaSize(m *tg.Message) int64 {
	switch media := m.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return 0
		}
		return doc.Size
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return 0
		}
		// 取最大尺寸的估算大小
		var best int64
		for _, s := range photo.Sizes {
			if ps, ok := s.(*tg.PhotoSizeProgressive); ok {
				if sz := ps.Sizes[len(ps.Sizes)-1]; int64(sz) > best {
					best = int64(sz)
				}
			} else if ps, ok := s.(*tg.PhotoSize); ok {
				est := int64(ps.W) * int64(ps.H) / 4
				if est > best {
					best = est
				}
			}
		}
		return best
	default:
		return 0
	}
}

func peerTitle(peer tg.InputPeerClass) string {
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		return fmt.Sprintf("channel-%d", p.ChannelID)
	case *tg.InputPeerUser:
		return fmt.Sprintf("user-%d", p.UserID)
	case *tg.InputPeerChat:
		return fmt.Sprintf("chat-%d", p.ChatID)
	default:
		return "unknown"
	}
}

// pickResolvedPeer 从 ContactsResolveUsername 的结果里取第一个可用 peer 与其标题。
func pickResolvedPeer(resolve *tg.ContactsResolvedPeer) (tg.InputPeerClass, string) {
	titles := map[int64]string{}
	for _, c := range resolve.Chats {
		switch ch := c.(type) {
		case *tg.Channel:
			titles[ch.ID] = ch.Title
		case *tg.Chat:
			titles[ch.ID] = ch.Title
		}
	}
	for _, u := range resolve.Users {
		if user, ok := u.(*tg.User); ok {
			titles[user.ID] = strings.TrimSpace(user.FirstName + " " + user.LastName)
		}
	}
	switch p := resolve.Peer.(type) {
	case *tg.PeerChannel:
		return &tg.InputPeerChannel{ChannelID: p.ChannelID, AccessHash: channelHash(resolve, p.ChannelID)}, titles[p.ChannelID]
	case *tg.PeerUser:
		return &tg.InputPeerUser{UserID: p.UserID, AccessHash: userHash(resolve, p.UserID)}, titles[p.UserID]
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, titles[p.ChatID]
	default:
		return nil, ""
	}
}

func channelHash(resolve *tg.ContactsResolvedPeer, id int64) int64 {
	for _, c := range resolve.Chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == id {
			return ch.AccessHash
		}
	}
	return 0
}

// DirNameForURL 返回该链接的下载子目录名（已 sanitize），实现
// 「按频道名自动分目录」：优先用探测到的频道标题；探测失败时从
// 链接本身回退——私有频道 c/<id> 用「频道 <id>」，公开频道用
// username。链接不是 t.me 格式或 sanitize 后为空则返回空串
// （调用方应退回不分子目录的行为）。
func DirNameForURL(raw, chatTitle string) string {
	if s := SanitizeDirName(chatTitle); s != "" {
		return s
	}
	raw = strings.TrimSpace(raw)
	if m := tmeRe.FindStringSubmatch(raw); m != nil {
		if m[2] != "" { // c/<internal_id>
			return SanitizeDirName(fmt.Sprintf("频道 %s", m[2]))
		}
		return SanitizeDirName(m[3])
	}
	return ""
}

// dirNameSanitizeRe 命中各文件系统共有的非法字符与控制字符。
var dirNameSanitizeRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// SanitizeDirName 将频道标题转为安全的目录名：非法字符替换为空格，
// 去掉首尾空白与结尾的点（Windows 兼容），压缩连续空白，限长 80
// 字符（按 rune 截断，避免中文标题被截出半个字）。处理结果为空则返回空串。
func SanitizeDirName(name string) string {
	s := dirNameSanitizeRe.ReplaceAllString(strings.TrimSpace(name), " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ". ")
	if s == "" || s == ".." {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 80 {
		s = string(runes[:80])
	}
	return s
}

func userHash(resolve *tg.ContactsResolvedPeer, id int64) int64 {
	for _, u := range resolve.Users {
		if user, ok := u.(*tg.User); ok && user.ID == id {
			return user.AccessHash
		}
	}
	return 0
}
