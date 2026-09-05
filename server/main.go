// tg-download 后端：封装 tdl 的下载管理服务。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"tg-download/internal/api"
	"tg-download/internal/config"
	"tg-download/internal/settings"
	"tg-download/internal/store"
	"tg-download/internal/task"
)

// version 由构建注入：go build -ldflags "-X main.version=0.3.1"
var version = "dev"

func main() {
	cfg := config.Default()
	cfg.BindFlags()
	flag.Parse()

	api.Version = version

	log.Printf("tg-download server starting: addr=%s data=%s tdl=%s", cfg.Addr, cfg.DataDir, cfg.TdlPath)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	if err := os.MkdirAll(cfg.TdlDataDir, 0o755); err != nil {
		log.Fatalf("create tdl data dir: %v", err)
	}

	// tdl v0.20.4 的 consts 包在 init 时读取 $HOME，缺失直接 panic
	// （"panic: $HOME is not defined"，表现为任务秒失败 exit status 2）。
	// fnOS 服务环境拉起的应用没有 HOME；tdl 实际存储由 --storage 显式指定，
	// 这里补一个可写目录仅为满足其初始化检查。
	if os.Getenv("HOME") == "" {
		os.Setenv("HOME", cfg.DataDir)
		log.Printf("HOME not set in environment, fallback to %s (tdl requires $HOME)", cfg.DataDir)
	}

	st := settings.NewStore(filepath.Join(cfg.DataDir, "settings.json"))
	if err := st.Load(); err != nil {
		log.Printf("load settings: %v (using defaults)", err)
	}

	// 任务持久化：优先数据库（-db-dsn），失败或未配置时回退 JSON 文件。
	var persist task.Persister
	if dsn := cfg.DBDSN; dsn != "" {
		p, err := store.NewMySQL(dsn)
		if err != nil {
			log.Printf("WARN: open task db failed (%v), falling back to json file", err)
		} else {
			persist = p
			log.Printf("task persistence: MariaDB/MySQL (%s)", cfg.DBAddr())
		}
	}
	if persist == nil {
		persist = task.NewJSONPersister(cfg.DataDir)
		log.Printf("task persistence: json file (%s)", filepath.Join(cfg.DataDir, "tasks.json"))
	}

	tm := task.NewManager(task.Options{
		Settings:   st,
		DataDir:    cfg.DataDir,
		TdlDataDir: cfg.TdlDataDir,
		Namespace:  cfg.Namespace,
		TdlPath:    cfg.TdlPath,
		Aria2Path:  cfg.Aria2Path,
		Persist:    persist,
	})
	if err := tm.Load(); err != nil {
		log.Printf("load tasks: %v", err)
	}

	srv := api.New(cfg, st, tm)
	mux := http.NewServeMux()
	srv.Register(mux)
	srv.ServeStatic(mux)

	httpServer := &http.Server{Addr: cfg.Addr, Handler: mux}

	// 优雅退出：停止 HTTP 与所有下载子进程
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")
		_ = httpServer.Close()
		tm.Shutdown()
		os.Exit(0)
	}()

	log.Printf("listening on http://%s", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
