// Package store 提供任务持久化的数据库实现。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tg-download/internal/task"
)

// NewMySQL 打开 MariaDB/MySQL 连接并建表，返回任务持久化实现。
// dsn 形如：user:pass@tcp(192.168.1.2:3306)/test?charset=utf8mb4&parseTime=false&loc=Local
func NewMySQL(dsn string) (task.Persister, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id              VARCHAR(64)  NOT NULL PRIMARY KEY,
		name            VARCHAR(512) NOT NULL DEFAULT '',
		url             TEXT         NULL,
		chat_title      VARCHAR(512) NOT NULL DEFAULT '',
		message_id      BIGINT       NOT NULL DEFAULT 0,
		kind            VARCHAR(16)  NOT NULL DEFAULT '',
		group_flag      TINYINT(1)   NOT NULL DEFAULT 0,
		total_size      BIGINT       NOT NULL DEFAULT 0,
		downloaded_size BIGINT       NOT NULL DEFAULT 0,
		speed           BIGINT       NOT NULL DEFAULT 0,
		progress        DOUBLE       NOT NULL DEFAULT 0,
		status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
		save_path       VARCHAR(768) NOT NULL DEFAULT '',
		created_at      BIGINT       NOT NULL DEFAULT 0,
		finished_at     BIGINT       NOT NULL DEFAULT 0,
		error           TEXT         NULL,
		files           MEDIUMTEXT   NULL,
		INDEX idx_status (status),
		INDEX idx_created (created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// 旧库缺少后续版本新增的列时自动补充（CREATE TABLE IF NOT EXISTS 不会改表结构）
	if err := addMissingColumns(db, "tasks", map[string]string{
		"peer_id":  "BIGINT NOT NULL DEFAULT 0",
		"selected": "MEDIUMTEXT NULL",
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate columns: %w", err)
	}
	// 链接台账：每条 URL 最近一次任务的快照，用于解析链接时提示"已下载过"。
	// 独立于 tasks 表，清空历史不会删除，跨会话可查。
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS download_urls (
		url         VARCHAR(768) NOT NULL PRIMARY KEY,
		status      VARCHAR(16)  NOT NULL DEFAULT '',
		name        VARCHAR(512) NOT NULL DEFAULT '',
		finished_at BIGINT       NOT NULL DEFAULT 0,
		updated_at  BIGINT       NOT NULL DEFAULT 0
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate download_urls: %w", err)
	}
	return &mysqlStore{db: db}, nil
}

// addMissingColumns 为已存在的表补充缺失列（幂等）。
func addMissingColumns(db *sql.DB, table string, cols map[string]string) error {
	rows, err := db.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for col, def := range cols {
		if have[col] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def)); err != nil {
			return err
		}
	}
	return nil
}

type mysqlStore struct {
	db *sql.DB
}

const upsertSQL = `INSERT INTO tasks
	(id, name, url, chat_title, message_id, kind, group_flag, total_size, downloaded_size,
	 speed, progress, status, save_path, created_at, finished_at, error, files,
	 peer_id, selected)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON DUPLICATE KEY UPDATE
		name=VALUES(name), url=VALUES(url), chat_title=VALUES(chat_title),
		message_id=VALUES(message_id), kind=VALUES(kind), group_flag=VALUES(group_flag),
		total_size=VALUES(total_size), downloaded_size=VALUES(downloaded_size),
		speed=VALUES(speed), progress=VALUES(progress), status=VALUES(status),
		save_path=VALUES(save_path), created_at=VALUES(created_at),
		finished_at=VALUES(finished_at), error=VALUES(error), files=VALUES(files),
		peer_id=VALUES(peer_id), selected=VALUES(selected)`

const upsertURLSQL = `INSERT INTO download_urls (url, status, name, finished_at, updated_at)
	VALUES (?,?,?,?,?)
	ON DUPLICATE KEY UPDATE
		status      = IF(download_urls.status = 'completed' AND VALUES(status) <> 'completed', download_urls.status, VALUES(status)),
		name        = IF(download_urls.status = 'completed' AND VALUES(status) <> 'completed', download_urls.name, VALUES(name)),
		finished_at = IF(download_urls.status = 'completed' AND VALUES(status) <> 'completed', download_urls.finished_at, VALUES(finished_at)),
		updated_at  = VALUES(updated_at)`

// UpsertURLs 同步链接台账：每条任务 URL 记录最近一次状态快照。
// 已是 completed 的记录不被非 completed 状态覆盖（下载过的链接始终显示"已下载"）。
func (s *mysqlStore) UpsertURLs(tasks []*task.Task) {
	now := time.Now().Unix()
	stmt, err := s.db.Prepare(upsertURLSQL)
	if err != nil {
		log.Printf("upsert urls prepare: %v", err)
		return
	}
	defer stmt.Close()
	for _, t := range tasks {
		if t.URL == "" {
			continue
		}
		fin := t.FinishedAt
		if fin == 0 {
			fin = now
		}
		if _, err := stmt.Exec(t.URL, string(t.Status), t.Name, fin, now); err != nil {
			log.Printf("upsert url %s: %v", t.URL, err)
		}
	}
}

// LookupURLs 批量查询链接台账，返回 url → 台账项；查不到的 url 不在 map 中。
func (s *mysqlStore) LookupURLs(urls []string) (map[string]task.URLEntry, error) {
	out := make(map[string]task.URLEntry, len(urls))
	if len(urls) == 0 {
		return out, nil
	}
	stmt := `SELECT url, status, name, finished_at FROM download_urls WHERE url IN (` +
		strings.Repeat("?,", len(urls)-1) + `?)`
	args := make([]any, len(urls))
	for i, u := range urls {
		args[i] = u
	}
	rows, err := s.db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e task.URLEntry
		if err := rows.Scan(&e.URL, &e.Status, &e.Name, &e.FinishedAt); err != nil {
			return out, err
		}
		out[e.URL] = e
	}
	return out, rows.Err()
}

func (s *mysqlStore) Save(tasks []*task.Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(upsertSQL)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, t := range tasks {
		files, err := json.Marshal(t.Files)
		if err != nil {
			files = []byte("[]")
		}
		var errStr, filesStr any
		if t.Error != "" {
			errStr = t.Error
		}
		if len(t.Files) > 0 {
			filesStr = string(files)
		} else {
			filesStr = "[]"
		}
		// 选中下载的消息 ID 列表（为空表示整条链接全量下载）
		var selStr any
		if len(t.Selected) > 0 {
			if b, err := json.Marshal(t.Selected); err == nil {
				selStr = string(b)
			}
		}
		if _, err := stmt.Exec(t.ID, t.Name, t.URL, t.ChatTitle, t.MessageID, t.Kind,
			t.Group, t.TotalSize, t.DownloadedSize, t.Speed, t.Progress, string(t.Status),
			t.SavePath, t.CreatedAt, t.FinishedAt, errStr, filesStr,
			t.PeerID, selStr); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 同步链接台账（失败不影响任务持久化）
	s.UpsertURLs(tasks)
	return nil
}

func (s *mysqlStore) Load() ([]*task.Task, error) {
	rows, err := s.db.Query(`SELECT id, name, url, chat_title, message_id, kind, group_flag,
		total_size, downloaded_size, speed, progress, status, save_path,
		created_at, finished_at, error, files, peer_id, selected
		FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*task.Task
	for rows.Next() {
		var (
			t        task.Task
			status   string
			group    int
			errStr   sql.NullString
			filesJS  sql.NullString
			peerID   sql.NullInt64
			selected sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.URL, &t.ChatTitle, &t.MessageID, &t.Kind,
			&group, &t.TotalSize, &t.DownloadedSize, &t.Speed, &t.Progress, &status,
			&t.SavePath, &t.CreatedAt, &t.FinishedAt, &errStr, &filesJS,
			&peerID, &selected); err != nil {
			return nil, err
		}
		t.Status = task.Status(status)
		t.Group = group != 0
		t.Error = errStr.String
		t.PeerID = peerID.Int64
		if selected.Valid && selected.String != "" {
			_ = json.Unmarshal([]byte(selected.String), &t.Selected)
		}
		if filesJS.Valid {
			_ = json.Unmarshal([]byte(filesJS.String), &t.Files)
		}
		if t.Files == nil {
			t.Files = []task.File{}
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *mysqlStore) Delete(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`DELETE FROM tasks WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) DeleteFinished() error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE status NOT IN ('pending','downloading','paused')`)
	return err
}

func (s *mysqlStore) Close() error { return s.db.Close() }
