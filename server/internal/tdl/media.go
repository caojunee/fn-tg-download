package tdl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// Resource 一个可勾选的媒体资源（与前端 Resource 结构对齐）。
type Resource struct {
	// Key 全局唯一键「<peerID>_<msgID>」，同时作为缩略图缓存名，
	// 与 tdl 落盘文件名模板 {对话ID}_{消息ID}_{文件名} 的前两段一致。
	Key      string `json:"key"`
	MsgID    int    `json:"msgId"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mime     string `json:"mime"`
	Kind     string `json:"kind"` // photo | video | audio | document
	Date     int64  `json:"date"`
	HasThumb bool   `json:"hasThumb"`
	ThumbURL string `json:"thumbUrl,omitempty"`
}

// ResourceSet 一条链接解析出的资源清单，供前端勾选下载。
type ResourceSet struct {
	URL       string      `json:"url"`
	ChatTitle string      `json:"chatTitle"`
	PeerID    int64       `json:"peerId"` // tdl 语义的会话 ID，生成 -f JSON 时用
	Kind      string      `json:"kind"`   // channel | album | single | range
	Total     int         `json:"total"`
	Resources []*Resource `json:"resources"`
	Error     string      `json:"error,omitempty"`
}

// 整频道枚举时的分页与保护参数。
const (
	// historyBatch 单次 getHistory 拉取条数（Telegram 侧上限 100）。
	historyBatch = 100
	// historyMax 单次枚举的消息总条数上限，防止超大频道无限拉取。
	// 达到上限即停止（已有结果照常返回）。
	historyMax = 20000
	// historyPause 分页之间的间隔，降低触发 FLOOD_WAIT 的概率。
	historyPause = 60 * time.Millisecond
)

// ListResources 解析一条链接并列出其中可选的媒体资源。
// 支持：整频道链接（全量枚举）、消息链接（相册 / 组合消息展开）、
// 消息范围链接（t.me/x/1/100）。
func ListResources(ctx context.Context, cfg ClientConfig, raw string) *ResourceSet {
	rs := &ResourceSet{URL: strings.TrimSpace(raw)}

	client, kv, err := newClient(ctx, cfg, false)
	if err != nil {
		rs.Error = err.Error()
		return rs
	}
	defer kv.Close()

	err = client.Run(ctx, func(ctx context.Context) error {
		api := tg.NewClient(client)
		fillResourceSet(ctx, api, kv, rs)
		return nil
	})
	if err != nil && rs.Error == "" {
		rs.Error = err.Error()
	}
	return rs
}

// fillResourceSet 在已建立的连接里填充资源清单。
func fillResourceSet(ctx context.Context, api *tg.Client, kv *BoltKV, rs *ResourceSet) {
	// 整频道链接
	if chat, private, ok := ChannelLink(rs.URL); ok {
		var peer tg.InputPeerClass
		var peerID int64
		var err error
		if private {
			peer, peerID, rs.ChatTitle, err = resolvePrivateChannel(ctx, api, kv, chat)
		} else {
			peer, peerID, rs.ChatTitle, err = resolveUsername(ctx, api, kv, chat)
		}
		if err != nil {
			rs.Error = err.Error()
			return
		}
		msgs, err := fetchAllHistory(ctx, api, peer)
		if err != nil {
			rs.Error = err.Error()
			return
		}
		rs.Kind = "channel"
		rs.PeerID = peerID
		rs.Resources = buildResources(peerID, msgs, rs.URL)
		rs.Total = len(rs.Resources)
		return
	}

	target, err := parseTmeURL(rs.URL)
	if err != nil {
		rs.Error = err.Error()
		return
	}

	var peer tg.InputPeerClass
	var peerID int64
	if target.internalID != 0 {
		peer, peerID, rs.ChatTitle, err = resolvePrivateChannel(ctx, api, kv, fmt.Sprintf("%d", target.internalID))
	} else {
		peer, peerID, rs.ChatTitle, err = resolveUsername(ctx, api, kv, target.username)
	}
	if err != nil {
		rs.Error = err.Error()
		return
	}
	rs.PeerID = peerID

	// 消息范围链接：按区间枚举
	if target.endMsg > target.msg {
		from, to := target.msg, target.endMsg
		if to-from > historyMax {
			to = from + historyMax
		}
		var msgs []*tg.Message
		for start := from; start <= to; start += historyBatch {
			end := start + historyBatch - 1
			if end > to {
				end = to
			}
			part, err := fetchMessageRange(ctx, api, peer, start, end)
			if err != nil {
				break
			}
			msgs = append(msgs, part...)
		}
		rs.Kind = "range"
		rs.Resources = buildResources(peerID, msgs, rs.URL)
		rs.Total = len(rs.Resources)
		return
	}

	// 单条消息：相册则展开整组
	msgs, err := fetchMessages(ctx, api, peer, target.msg)
	if err != nil {
		rs.Error = fmt.Sprintf("获取消息失败: %v", err)
		return
	}
	main := findMessage(msgs, target.msg)
	if main == nil {
		rs.Error = "消息不存在（可能已删除，或当前账号无权限查看）"
		return
	}
	if !hasMedia(main) {
		rs.Error = "该消息不含可下载媒体（纯文本）"
		return
	}

	picked := []*tg.Message{main}
	rs.Kind = "single"
	if main.GroupedID != 0 {
		from, to := target.msg-20, target.msg+20
		if from < 1 {
			from = 1
		}
		if nearby, err := fetchMessageRange(ctx, api, peer, from, to); err == nil {
			group := make([]*tg.Message, 0, 4)
			for _, m := range nearby {
				if m.GroupedID == main.GroupedID && hasMedia(m) {
					group = append(group, m)
				}
			}
			if len(group) > 1 {
				picked = group
				rs.Kind = "album"
			}
		}
	}
	rs.Resources = buildResources(peerID, picked, rs.URL)
	rs.Total = len(rs.Resources)
}

// fetchAllHistory 分页拉取会话的全部消息，返回其中带媒体的部分。
// 达到 historyMax 或取完（返回条数不足一页）时停止。
func fetchAllHistory(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) ([]*tg.Message, error) {
	var (
		out     []*tg.Message
		offset  int
		scanned int
	)
	for {
		if scanned >= historyMax {
			break
		}
		batch, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:      peer,
			OffsetID:  offset,
			Limit:     historyBatch,
			MaxID:     0,
			MinID:     0,
			AddOffset: 0,
			Hash:      0,
		})
		if err != nil {
			// 已取到部分结果时不让整次枚举失败，交给上层用已有结果继续
			if len(out) > 0 {
				return out, nil
			}
			return nil, fmt.Errorf("获取历史消息失败: %w", err)
		}
		var msgs []*tg.Message
		switch v := batch.(type) {
		case *tg.MessagesMessages:
			msgs = filterMessages(v.Messages)
		case *tg.MessagesMessagesSlice:
			msgs = filterMessages(v.Messages)
		case *tg.MessagesChannelMessages:
			msgs = filterMessages(v.Messages)
		case *tg.MessagesMessagesNotModified:
			return out, nil
		default:
			if len(out) > 0 {
				return out, nil
			}
			return nil, fmt.Errorf("unexpected messages response type %T", batch)
		}
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			if hasMedia(m) {
				out = append(out, m)
			}
		}
		scanned += len(msgs)
		last := msgs[len(msgs)-1]
		if last.ID <= offset {
			break // 偏移未推进，避免死循环
		}
		offset = last.ID
		if len(msgs) < historyBatch {
			break
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(historyPause):
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("该会话没有可下载的媒体消息")
	}
	return out, nil
}

// buildResources 把消息转成资源条目，按消息 ID 倒序（新→旧），
// 与 Telegram 浏览习惯一致。
func buildResources(peerID int64, msgs []*tg.Message, url string) []*Resource {
	out := make([]*Resource, 0, len(msgs))
	for _, m := range msgs {
		if !hasMedia(m) {
			continue
		}
		r := &Resource{
			Key:   fmt.Sprintf("%d_%d", peerID, m.ID),
			MsgID: m.ID,
			Size:  mediaSize(m),
			Date:  int64(m.Date) * 1000,
			Name:  messageFileName(m),
		}
		r.Kind, r.Mime = mediaKindAndMime(m)
		_, r.HasThumb = thumbLocation(m)
		if r.HasThumb {
			r.ThumbURL = fmt.Sprintf("/api/resource/thumb?key=%s&url=%s", r.Key, urlEncode(url))
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MsgID > out[j].MsgID })
	return out
}

// urlEncode 对链接做 query 转义（缩略图 URL 里回传原始链接）。
func urlEncode(s string) string {
	return strings.NewReplacer(
		"%", "%25", " ", "%20", "&", "%26", "?", "%3F", "#", "%23", "=", "%3D", "+", "%2B",
	).Replace(s)
}

// messageFileName 提取媒体文件名：优先 document 的文件名属性，
// 其次回退到「类型 + 消息 ID」的可读名。
func messageFileName(m *tg.Message) string {
	if doc := messageDocument(m); doc != nil {
		for _, attr := range doc.Attributes {
			if a, ok := attr.(*tg.DocumentAttributeFilename); ok && a.FileName != "" {
				return a.FileName
			}
		}
	}
	kind, _ := mediaKindAndMime(m)
	switch kind {
	case "photo":
		return fmt.Sprintf("photo_%d.jpg", m.ID)
	case "video":
		return fmt.Sprintf("video_%d.mp4", m.ID)
	case "audio":
		return fmt.Sprintf("audio_%d", m.ID)
	default:
		return fmt.Sprintf("file_%d", m.ID)
	}
}

// messageDocument 取消息里的 document（视频 / 文件 / 音频等）。
func messageDocument(m *tg.Message) *tg.Document {
	media, ok := m.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return nil
	}
	return doc
}

// mediaKindAndMime 判定资源类型（photo / video / audio / document）与 MIME。
func mediaKindAndMime(m *tg.Message) (kind, mime string) {
	if _, ok := m.Media.(*tg.MessageMediaPhoto); ok {
		return "photo", "image/jpeg"
	}
	doc := messageDocument(m)
	if doc == nil {
		return "document", ""
	}
	mime = doc.MimeType
	switch {
	case strings.HasPrefix(mime, "video/"):
		return "video", mime
	case strings.HasPrefix(mime, "audio/"), strings.HasPrefix(mime, "application/ogg"):
		return "audio", mime
	case strings.HasPrefix(mime, "image/"):
		return "photo", mime
	default:
		return "document", mime
	}
}

// thumbOrder 缩略图尺寸优先级：m（320px）足够清晰且体积小，
// 逐级回退到其它尺寸。
var thumbOrder = []string{"m", "s", "x", "y", "w", "a", "b", "c", "d"}

// thumbLocation 构造缩略图的下载位置。返回 nil 表示该媒体没有可用缩略图。
// 注意：文件引用（file reference）有时效，故每次需要取图时都重新
// 拉取消息后再构造，不能长期缓存本结构。
func thumbLocation(m *tg.Message) (tg.InputFileLocationClass, bool) {
	if photo, ok := m.Media.(*tg.MessageMediaPhoto); ok {
		if p, ok := photo.Photo.(*tg.Photo); ok {
			if t := pickThumbType(p.Sizes); t != "" {
				return &tg.InputPhotoFileLocation{
					ID:            p.ID,
					AccessHash:    p.AccessHash,
					FileReference: p.FileReference,
					ThumbSize:     t,
				}, true
			}
		}
	}
	if doc := messageDocument(m); doc != nil {
		if t := pickThumbType(doc.Thumbs); t != "" {
			return &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
				ThumbSize:     t,
			}, true
		}
	}
	return nil, false
}

// pickThumbType 按优先级挑一个可用的缩略图尺寸类型。
func pickThumbType(sizes []tg.PhotoSizeClass) string {
	avail := map[string]bool{}
	for _, s := range sizes {
		if t := s.GetType(); t != "" {
			avail[t] = true
		}
	}
	for _, want := range thumbOrder {
		if avail[want] {
			return want
		}
	}
	// 都不匹配时取第一个（常见的还有 PhotoSizeProgressive 的自定义类型）
	for _, s := range sizes {
		if t := s.GetType(); t != "" {
			return t
		}
	}
	return ""
}

// ---- 缩略图缓存 ----

// ThumbCacheDir 缩略图缓存目录（位于服务端 data 目录下）。
func ThumbCacheDir(dataDir string) string {
	return filepath.Join(dataDir, "thumbs")
}

// thumbCachePaths 返回缓存文件路径与其 content-type 记录文件。
func thumbCachePaths(dataDir, key string) (string, string) {
	base := filepath.Join(ThumbCacheDir(dataDir), sanitizeKey(key))
	return base + ".img", base + ".ct"
}

// sanitizeKey 把缓存键收敛为安全文件名（key 形如 4434014100_2）。
func sanitizeKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// CachedThumb 读取已缓存的缩略图，未命中返回 nil。
func CachedThumb(dataDir, key string) (data []byte, contentType string, ok bool) {
	imgPath, ctPath := thumbCachePaths(dataDir, key)
	data, err := os.ReadFile(imgPath)
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	if ct, err := os.ReadFile(ctPath); err == nil && len(ct) > 0 {
		contentType = string(ct)
	}
	if contentType == "" {
		contentType = sniffContentType(data)
	}
	return data, contentType, true
}

// ThumbOptions 取缩略图的参数。
type ThumbOptions struct {
	URL    string // 原始链接，用于定位会话
	Key    string // <peerID>_<msgID>，仅作缓存键
	PeerID int64
	MsgID  int
}

// FetchThumb 按需下载一条消息的缩略图：优先读磁盘缓存，
// 未命中则重新拉取该消息（保证 file reference 新鲜）后下载。
// 返回图片字节与 content-type。
func FetchThumb(ctx context.Context, cfg ClientConfig, dataDir string, o ThumbOptions) ([]byte, string, error) {
	if data, ct, ok := CachedThumb(dataDir, o.Key); ok {
		return data, ct, nil
	}

	var (
		data []byte
		err  error
	)
	client, kv, cerr := newClient(ctx, cfg, false)
	if cerr != nil {
		return nil, "", cerr
	}
	defer kv.Close()

	err = client.Run(ctx, func(ctx context.Context) error {
		api := tg.NewClient(client)

		var peer tg.InputPeerClass
		if chat, private, ok := ChannelLink(o.URL); ok {
			if private {
				peer, _, _, err = resolvePrivateChannel(ctx, api, kv, chat)
			} else {
				peer, _, _, err = resolveUsername(ctx, api, kv, chat)
			}
		} else if target, terr := parseTmeURL(o.URL); terr == nil {
			if target.internalID != 0 {
				peer, _, _, err = resolvePrivateChannel(ctx, api, kv, fmt.Sprintf("%d", target.internalID))
			} else {
				peer, _, _, err = resolveUsername(ctx, api, kv, target.username)
			}
		} else {
			err = terr
		}
		if err != nil {
			return err
		}
		if peer == nil {
			return fmt.Errorf("无法解析链接对应的会话")
		}

		msgs, ferr := fetchMessages(ctx, api, peer, int64(o.MsgID))
		if ferr != nil {
			return ferr
		}
		m := findMessage(msgs, int64(o.MsgID))
		if m == nil {
			return fmt.Errorf("消息不存在或已删除")
		}
		loc, ok := thumbLocation(m)
		if !ok {
			return fmt.Errorf("该媒体没有缩略图")
		}

		var buf writeBuffer
		if _, err = client.Download(loc).Stream(ctx, &buf); err != nil {
			return err
		}
		if len(buf.b) == 0 {
			return fmt.Errorf("缩略图为空")
		}
		data = buf.b
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	ct := sniffContentType(data)
	saveThumbCache(dataDir, o.Key, data, ct)
	return data, ct, nil
}

// writeBuffer 收集下载流到内存（缩略图体积很小，直接缓冲即可）。
type writeBuffer struct{ b []byte }

func (w *writeBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func saveThumbCache(dataDir, key string, data []byte, contentType string) {
	imgPath, ctPath := thumbCachePaths(dataDir, key)
	if err := os.MkdirAll(filepath.Dir(imgPath), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(imgPath, data, 0o644); err != nil {
		return
	}
	_ = os.WriteFile(ctPath, []byte(contentType), 0o644)
}

// sniffContentType 按文件头判断图片类型，兜底为 jpeg。
func sniffContentType(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8:
		return "image/jpeg"
	case len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return "image/png"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) >= 6 && string(b[0:3]) == "GIF":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// ---- 选中项下载清单 ----

// selectionJSON 与 tdl chat export 输出结构一致：
// tdl 的 -f 解析只依赖顶层 id（会话 ID）与每条消息的 id/type/file。
type selectionJSON struct {
	ID       int64          `json:"id"`
	Messages []selectionMsg `json:"messages"`
}

type selectionMsg struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	File string `json:"file"`
	Date int64  `json:"date,omitempty"`
}

// WriteSelectionJSON 生成只含指定消息的下载清单，供 tdl dl -f 使用。
// msgIDs 为空则视为「全部」，返回错误。
func WriteSelectionJSON(path string, peerID int64, msgIDs []int) error {
	if len(msgIDs) == 0 {
		return fmt.Errorf("no message selected")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create selection dir: %w", err)
	}
	sel := selectionJSON{ID: peerID, Messages: make([]selectionMsg, 0, len(msgIDs))}
	for _, id := range msgIDs {
		if id <= 0 {
			continue
		}
		sel.Messages = append(sel.Messages, selectionMsg{
			ID:   id,
			Type: "message",
			File: "selected", // tdl 的 -f 仅用该字段做「是否媒体」过滤，实际文件名取自消息本身
		})
	}
	if len(sel.Messages) == 0 {
		return fmt.Errorf("no valid message selected")
	}
	b, err := json.Marshal(sel)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ---- peer 解析（与探测阶段共用逻辑） ----

// resolvePrivateChannel 解析私有频道（t.me/c/<id>）：
// access hash 来自 tdl 的 peers 缓存；解析成功后回写缓存，
// 保证后续 tdl dl -f 也能解析同一会话。
func resolvePrivateChannel(ctx context.Context, api *tg.Client, kv *BoltKV, chat string) (tg.InputPeerClass, int64, string, error) {
	id, err := ato64err(chat)
	if err != nil {
		return nil, 0, "", fmt.Errorf("私有频道 ID 无效: %s", chat)
	}
	hash, ok := cachedChannelHash(kv, id)
	if !ok || hash == 0 {
		return nil, 0, "", fmt.Errorf("频道 %d 暂无访问权限缓存，请先下载一次该频道内容以建立缓存", id)
	}
	rememberPeer(ctx, kv, "channel_", id, hash)
	title := channelTitle(ctx, api, id, hash)
	if title == "" {
		title = fmt.Sprintf("频道 %d", id)
	}
	return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, id, title, nil
}

// resolveUsername 解析公开频道 / 用户（t.me/<username>）：
// 解析成功后把 access hash 写入 tdl 的 peers 缓存，
// 使 tdl dl -f 能按会话 ID 重新定位该 peer。
func resolveUsername(ctx context.Context, api *tg.Client, kv *BoltKV, username string) (tg.InputPeerClass, int64, string, error) {
	resolve, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, 0, "", fmt.Errorf("解析 %s 失败: %w", username, err)
	}
	peer, title := pickResolvedPeer(resolve)
	if peer == nil {
		return nil, 0, "", fmt.Errorf("无法解析 %s", username)
	}
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		rememberPeer(ctx, kv, "channel_", p.ChannelID, p.AccessHash)
		if title == "" {
			title = username
		}
		return peer, p.ChannelID, title, nil
	case *tg.InputPeerUser:
		rememberPeer(ctx, kv, "users_", p.UserID, p.AccessHash)
		if title == "" {
			title = username
		}
		return peer, p.UserID, title, nil
	default:
		return nil, 0, "", fmt.Errorf("暂不支持该会话类型: %s", username)
	}
}

// rememberPeer 把 access hash 写入 tdl 的 peers 缓存。
// 键格式与 tdl 内部一致（peers:key:<prefix>:<id>），
// 使得 tdl 后续用会话 ID 定位 peer 时能命中缓存。
func rememberPeer(ctx context.Context, kv *BoltKV, prefix string, id, hash int64) {
	if kv == nil || hash == 0 {
		return
	}
	b, err := json.Marshal(peerKVValue{AccessHash: hash})
	if err != nil {
		return
	}
	_ = kv.Set(ctx, fmt.Sprintf("peers:key:%s:%d", prefix, id), b)
}
