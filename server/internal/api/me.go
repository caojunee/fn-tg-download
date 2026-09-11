package api

// 登录信息缓存：/api/auth/me 不应每次都起 Telegram 客户端实测——
// 那要开 tdl bolt 存储锁（flock）+ 一次网络自检。下载运行期间 bolt
// 被 serve 进程独占，实测会阻塞到下载结束（表现为前端 auth/me 超时）。
//
// 策略：下载占锁期间直接回缓存；不占锁时照旧实测并刷新缓存（登录
// 状态被远端吊销等极端情况靠实测纠偏）。缓存持久化到 data/me.json
// （普通文件，不受 bolt flock 影响），服务重启后首次打开页面也秒回。

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tg-download/internal/tdl"
)

type meCache struct {
	mu   sync.Mutex
	path string
	info *tdl.UserInfo

	// 上次实测时间与后台刷新去重标记：/api/auth/me 改为"有缓存立即回、
	// 后台纠偏"后，用这两者避免频繁起 tdl 客户端 / 并发实测
	lastCheck  time.Time
	refreshing bool
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
	c.lastCheck = time.Now()
	if b, err := json.Marshal(info); err == nil {
		_ = os.WriteFile(c.path, b, 0o600)
	}
}

// refreshAsync 后台刷新登录信息：页面打开不再等实测（起 tdl 客户端 +
// bolt 锁 + 走代理的网络自检，秒级），远端吊销/换号等状态偏差由后台
// 实测纠偏。节流：距上次实测不足 1 分钟不重复；下载占锁（bolt flock）
// 期间不起，实测会干等锁白耗资源。实测失败不动缓存（可能是网络抖动，
// 真正的登录问题由下载/登录流程暴露）。
func (c *meCache) refreshAsync(serveBusy func() bool, live func(context.Context) (*tdl.UserInfo, error)) {
	c.mu.Lock()
	if c.refreshing || time.Since(c.lastCheck) < time.Minute {
		c.mu.Unlock()
		return
	}
	c.refreshing = true
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			c.refreshing = false
			c.mu.Unlock()
		}()
		if serveBusy() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := live(ctx)
		if err != nil {
			log.Printf("后台刷新登录信息失败（缓存保持不变）: %v", err)
			return
		}
		c.save(info)
	}()
}

// clear 清除内存与磁盘缓存（退出登录）。
func (c *meCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = nil
	_ = os.Remove(c.path)
}
