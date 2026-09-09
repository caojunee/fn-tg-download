package tdl

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// TempStorage 在下载任务独占 tdl bolt 存储锁期间，为探测/资源枚举提供
// 可用的存储副本：把 DataDir 整目录复制到同盘临时目录（文件很小，通常
// 几百 KB），返回替换了 DataDir 的配置与清理函数。
//
// 下载期间 tdl 对 bolt 的写入仅限 session/peers 缓存，复制出的副本
// 足以完成解析；若副本恰好拷到写入瞬间导致 bolt 校验失败，最多重试
// copyAttempts 次，仍失败则返回错误由调用方回退到原行为。
func TempStorage(cfg ClientConfig) (ClientConfig, func(), error) {
	noop := func() {}
	if cfg.DataDir == "" {
		return cfg, noop, fmt.Errorf("data dir is empty")
	}
	tmp := cfg.DataDir + fmt.Sprintf(".tmp-%d", time.Now().UnixNano())

	const copyAttempts = 3
	for i := 0; i < copyAttempts; i++ {
		if err := copyDir(cfg.DataDir, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			if i == copyAttempts-1 {
				return cfg, noop, fmt.Errorf("copy storage: %w", err)
			}
			continue
		}
		cpCfg := cfg
		cpCfg.DataDir = tmp
		return cpCfg, func() { _ = os.RemoveAll(tmp) }, nil
	}
	return cfg, noop, fmt.Errorf("copy storage: unreachable")
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // bolt 存储是单文件；子目录（如有）非必需
		}
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dst, e.Name()), os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
