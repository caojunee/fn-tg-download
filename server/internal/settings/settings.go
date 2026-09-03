// Package settings 提供系统设置的加载与保存（JSON 持久化）。
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	// 默认下载路径
	DownloadPath string `json:"downloadPath"`
	// 代理地址，如 http://192.168.31.180:20172
	Proxy string `json:"proxy"`
	// 默认并发数
	Concurrency int `json:"concurrency"`
	// 默认开启相册 / 组合消息探测
	AutoGroup bool `json:"autoGroup"`
	// 单文件大小上限（MB），0 不限制
	MaxSizeMB int `json:"maxSizeMB"`
	// 限速（KB/s），0 不限速
	SpeedLimitKB int `json:"speedLimitKB"`
	// tdl 可执行文件位置
	TdlPath string `json:"tdlPath"`
}

func Defaults() Settings {
	return Settings{
		DownloadPath: "downloads",
		Proxy:        "",
		Concurrency:  4,
		AutoGroup:    true,
		MaxSizeMB:    0,
		SpeedLimitKB: 0,
		TdlPath:      "tdl",
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	data Settings
}

func NewStore(path string) *Store {
	return &Store{path: path, data: Defaults()}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return err
	}
	// 兜底默认值
	if s.data.Concurrency <= 0 {
		s.data.Concurrency = Defaults().Concurrency
	}
	return nil
}

func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *Store) Save(next Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next.Concurrency <= 0 {
		next.Concurrency = Defaults().Concurrency
	}
	if next.Concurrency > 16 {
		next.Concurrency = 16
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return next, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return next, err
	}
	if err := os.WriteFile(s.path, b, 0o644); err != nil {
		return next, err
	}
	s.data = next
	return s.data, nil
}
