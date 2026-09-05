// Package config 提供服务运行时配置。
package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	// HTTP 监听地址
	Addr string
	// 服务自身数据目录（任务、设置 JSON）
	DataDir string
	// tdl 可执行文件路径
	TdlPath string
	// aria2c 可执行文件路径（serve+aria2 字节级续传下载器）
	Aria2Path string
	// tdl bolt 存储目录（对应 tdl --storage type=bolt,path=<TdlDataDir>）
	TdlDataDir string
	// tdl namespace，默认 default
	Namespace string
	// 前端构建产物目录（web/dist），为空则不托管静态文件
	WebDir string
	// 任务持久化 DSN（MariaDB/MySQL），为空时任务存 JSON 文件。
	// 示例：root:gkc2020..@tcp(192.168.1.2:3306)/test?charset=utf8mb4&loc=Local
	DBDSN string
}

// Default 基于 fpk 部署约定的默认配置。
// fpk 内目录布局：
//
//	/app/server            Go 后端
//	/app/bin/tdl           tdl 二进制
//	/app/web               前端 dist
//	/app/data              运行数据（settings.json / tasks.json / tdl-data）
func Default() Config {
	exe, _ := os.Executable()
	base := filepath.Dir(exe)
	if wd, err := os.Getwd(); err == nil {
		// 开发模式下优先以工作目录为基准
		base = wd
	}
	return Config{
		Addr:       "127.0.0.1:8080",
		DataDir:    filepath.Join(base, "data"),
		TdlPath:    findTdl(base),
		Aria2Path:  findBin(base, "aria2c"),
		TdlDataDir: filepath.Join(base, "data", "tdl-data"),
		Namespace:  "default",
		WebDir:     filepath.Join(base, "web"),
	}
}

func findTdl(base string) string {
	return findBin(base, "tdl")
}

func findBin(base, name string) string {
	candidates := []string{
		filepath.Join(base, "bin", name),
		filepath.Join(base, "bin", name+".exe"),
		filepath.Join(base, name),
		filepath.Join(base, name+".exe"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return name
}

// BindFlags 将配置绑定到命令行 flags。
func (c *Config) BindFlags() {
	flag.StringVar(&c.Addr, "addr", c.Addr, "HTTP listen address")
	flag.StringVar(&c.DataDir, "data-dir", c.DataDir, "server data dir (tasks/settings)")
	flag.StringVar(&c.TdlPath, "tdl", c.TdlPath, "path to tdl binary")
	flag.StringVar(&c.Aria2Path, "aria2", c.Aria2Path, "path to aria2c binary (byte-level resume downloader)")
	flag.StringVar(&c.TdlDataDir, "tdl-data", c.TdlDataDir, "tdl bolt storage dir (--storage path)")
	flag.StringVar(&c.Namespace, "ns", c.Namespace, "tdl namespace")
	flag.StringVar(&c.WebDir, "web", c.WebDir, "static web dist dir (empty to disable)")
	flag.StringVar(&c.DBDSN, "db-dsn", c.DBDSN, "MariaDB/MySQL DSN for task persistence (empty = json file)")
}

// DBAddr 从 DSN 中提取 host:port，仅用于日志展示。
func (c *Config) DBAddr() string {
	i := strings.Index(c.DBDSN, "tcp(")
	if i < 0 {
		return "(db)"
	}
	rest := c.DBDSN[i+4:]
	if j := strings.Index(rest, ")"); j >= 0 {
		return rest[:j]
	}
	return "(db)"
}
