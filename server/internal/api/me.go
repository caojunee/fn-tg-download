package api

// 登录信息缓存：/api/auth/me 不应每次都起 Telegram 客户端实测——
// 那要开 tdl bolt 存储锁（flock）+ 一次网络自检。下载运行期间 bolt
// 被 serve 进程独占，实测会阻塞到下载结束（表现为前端 auth/me 超时）。
//
// 策略：下载占锁期间直接回缓存；不占锁时照旧实测并刷新缓存（登录
// 状态被远端吊销等极端情况靠实测纠偏）。缓存持久化到 data/me.json
// （普通文件，不受 bolt flock 影响），服务重启后首次打开页面也秒回。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"tg-download/internal/tdl"
)

type meCache struct {
	mu   sync.Mutex
	path string
	info *tdl.UserInfo
}

func newMeCache(dataDir string) *meCache {
	return &meCache{path: filepath.Join(dataDir, "me.json")}
}

// load 返回缓存的登录信息（内存优先，其次 data/me.json），无缓存返回 nil。
func (c *meCache) load() *tdl.UserInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.info != nil {
		return c.info
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	var info tdl.UserInfo
	if json.Unmarshal(b, &info) != nil || info.ID == 0 {
		return nil
	}
	c.info = &info
	return c.info
}

// save 写入内存与磁盘缓存。
func (c *meCache) save(info *tdl.UserInfo) {
	if info == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = info
	if b, err := json.Marshal(info); err == nil {
		_ = os.WriteFile(c.path, b, 0o600)
	}
}

// clear 清除内存与磁盘缓存（退出登录）。
func (c *meCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = nil
	_ = os.Remove(c.path)
}
