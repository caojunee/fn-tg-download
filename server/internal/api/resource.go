package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-download/internal/task"
	"tg-download/internal/tdl"
)

// resourceCache 缓存最近一次资源枚举结果：
// 枚举整频道代价较高，勾选后创建任务时用缓存回填文件名与大小，
// 避免重复拉取一遍消息。
type resourceCache struct {
	mu   sync.Mutex
	data map[string]resourceCacheEntry
}

type resourceCacheEntry struct {
	set *tdl.ResourceSet
	at  time.Time
}

// resourceCacheTTL 资源清单缓存有效期。用户可能花较长时间挑选，
// 过期只导致文件名回退为占位名，不影响下载。
const resourceCacheTTL = 30 * time.Minute

func newResourceCache() *resourceCache {
	return &resourceCache{data: map[string]resourceCacheEntry{}}
}

func (c *resourceCache) put(url string, set *tdl.ResourceSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.data {
		if time.Since(v.at) > resourceCacheTTL {
			delete(c.data, k)
		}
	}
	c.data[url] = resourceCacheEntry{set: set, at: time.Now()}
}

func (c *resourceCache) get(url string) (*tdl.ResourceSet, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[url]
	if !ok || time.Since(e.at) > resourceCacheTTL {
		return nil, false
	}
	return e.set, true
}

// selectionFiles 按勾选顺序从缓存取回名称 / 大小；缓存未命中时
// 只用消息 ID 占位（下载本身不依赖这些名称）。
func (c *resourceCache) selectionFiles(url string, msgIDs []int) []task.File {
	set, _ := c.get(url)
	byID := map[int]*tdl.Resource{}
	if set != nil {
		for _, r := range set.Resources {
			byID[r.MsgID] = r
		}
	}
	out := make([]task.File, 0, len(msgIDs))
	for _, id := range msgIDs {
		f := task.File{ID: strconv.Itoa(id)}
		if r, ok := byID[id]; ok {
			f.ID = r.Key
			f.Name = r.Name
			f.Size = r.Size
		}
		out = append(out, f)
	}
	return out
}

// ---- 接口 ----

type listResourcesReq struct {
	URL string `json:"url"`
}

// handleListResources 列出一条链接里可选的全部媒体资源。
// 整频道会分页拉取全部消息，耗时随频道规模增长，故给足超时。
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	req, err := body[listResourcesReq](r)
	if err != nil || strings.TrimSpace(req.URL) == "" {
		fail(w, codeBizErr, "链接不能为空")
		return
	}
	ctx := rctx(5 * time.Minute)
	cfg := s.clientConfig()
	// 下载占锁期间用存储副本枚举资源（副本不可用则回退原行为）
	if s.tasks.ServeBusy() {
		if cpCfg, cleanup, err := tdl.TempStorage(cfg); err == nil {
			defer cleanup()
			cfg = cpCfg
		} else {
			log.Printf("resource list on temp storage failed, fallback: %v", err)
		}
	}
	set := tdl.ListResources(ctx, cfg, req.URL)
	if set.Error == "" && len(set.Resources) > 0 {
		s.resCache.put(set.URL, set)
		log.Printf("resource list: %s -> %d items (peer=%d kind=%s)",
			set.URL, set.Total, set.PeerID, set.Kind)
	}
	ok(w, set)
}

// handleResourceThumb 返回单条资源的缩略图（磁盘缓存优先，未命中实时下载）。
func (s *Server) handleResourceThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := strings.TrimSpace(q.Get("key"))
	raw := strings.TrimSpace(q.Get("url"))
	if key == "" || raw == "" {
		http.Error(w, "missing key or url", http.StatusBadRequest)
		return
	}
	// key 形如 <peerID>_<msgID>
	i := strings.LastIndex(key, "_")
	msgID, _ := strconv.Atoi(key[i+1:])
	peerID, _ := strconv.ParseInt(key[:i], 10, 64)
	if i <= 0 || msgID <= 0 {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}

	if data, ct, hit := tdl.CachedThumb(s.cfg.DataDir, key); hit {
		serveThumb(w, data, ct)
		return
	}
	ctx := rctx(60 * time.Second)
	thumbCfg := s.clientConfig()
	// 下载占锁期间用存储副本取缩略图（副本不可用则回退原行为）
	if s.tasks.ServeBusy() {
		if cpCfg, cleanup, err := tdl.TempStorage(thumbCfg); err == nil {
			defer cleanup()
			thumbCfg = cpCfg
		}
	}
	data, ct, err := tdl.FetchThumb(ctx, thumbCfg, s.cfg.DataDir, tdl.ThumbOptions{
		URL:    raw,
		Key:    key,
		PeerID: peerID,
		MsgID:  msgID,
	})
	if err != nil {
		http.Error(w, "缩略图获取失败: "+err.Error(), http.StatusNotFound)
		return
	}
	serveThumb(w, data, ct)
}

func serveThumb(w http.ResponseWriter, data []byte, contentType string) {
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
