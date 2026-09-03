// Package tdl 封装 tdl CLI 与其存储格式。
//
// tdl 的 bolt 驱动存储布局（见 tdl pkg/kv/bolt.go）：
//   - bolt 数据库文件：<path>/<namespace>（如 data/default）
//   - bucket 名 = namespace
//   - session key = "session"，value = gotd session.Loader 序列化的 JSON
//   - app key = "app"，code 登录后为 "desktop"
package tdl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// ErrNotFound 与 tdl core/storage 的语义一致。
var ErrNotFound = errors.New("key not found")

type Storage interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// BoltKV 是 tdl 兼容的 bolt 存储实现（单 namespace）。
type BoltKV struct {
	db *bolt.DB
	ns []byte
}

// OpenBolt 打开（必要时创建）指定目录下的 namespace 数据库。
func OpenBolt(dir, namespace string) (*BoltKV, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	db, err := bolt.Open(filepath.Join(dir, namespace), 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(namespace))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &BoltKV{db: db, ns: []byte(namespace)}, nil
}

func (b *BoltKV) Close() error { return b.db.Close() }

func (b *BoltKV) Get(_ context.Context, key string) ([]byte, error) {
	var out []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(b.ns).Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		out = bytes.Clone(v)
		return nil
	})
	return out, err
}

func (b *BoltKV) Set(_ context.Context, key string, value []byte) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(b.ns).Put([]byte(key), bytes.Clone(value))
	})
}

func (b *BoltKV) Delete(_ context.Context, key string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(b.ns).Delete([]byte(key))
	})
}

const (
	keySession = "session"
	keyApp     = "app"
)

// HasSession 判断命名空间下是否已有登录 session。
func HasSession(ctx context.Context, dir, namespace string) (bool, error) {
	kv, err := OpenBolt(dir, namespace)
	if err != nil {
		return false, err
	}
	defer kv.Close()
	_, err = kv.Get(ctx, keySession)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ClearSession 删除登录态（退出登录）。
func ClearSession(ctx context.Context, dir, namespace string) error {
	kv, err := OpenBolt(dir, namespace)
	if err != nil {
		return err
	}
	defer kv.Close()
	_ = kv.Delete(ctx, keySession)
	return kv.Delete(ctx, keyApp)
}
