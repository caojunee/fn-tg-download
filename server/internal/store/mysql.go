// Package store 提供任务持久化的数据库实现。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	return &mysqlStore{db: db}, nil
}

type mysqlStore struct {
	db *sql.DB
}

const upsertSQL = `INSERT INTO tasks
	(id, name, url, chat_title, message_id, kind, group_flag, total_size, downloaded_size,
	 speed, progress, status, save_path, created_at, finished_at, error, files)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON DUPLICATE KEY UPDATE
		name=VALUES(name), url=VALUES(url), chat_title=VALUES(chat_title),
		message_id=VALUES(message_id), kind=VALUES(kind), group_flag=VALUES(group_flag),
		total_size=VALUES(total_size), downloaded_size=VALUES(downloaded_size),
		speed=VALUES(speed), progress=VALUES(progress), status=VALUES(status),
		save_path=VALUES(save_path), created_at=VALUES(created_at),
		finished_at=VALUES(finished_at), error=VALUES(error), files=VALUES(files)`

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
		if _, err := stmt.Exec(t.ID, t.Name, t.URL, t.ChatTitle, t.MessageID, t.Kind,
			t.Group, t.TotalSize, t.DownloadedSize, t.Speed, t.Progress, string(t.Status),
			t.SavePath, t.CreatedAt, t.FinishedAt, errStr, filesStr); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) Load() ([]*task.Task, error) {
	rows, err := s.db.Query(`SELECT id, name, url, chat_title, message_id, kind, group_flag,
		total_size, downloaded_size, speed, progress, status, save_path,
		created_at, finished_at, error, files FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*task.Task
	for rows.Next() {
		var (
			t       task.Task
			status  string
			group   int
			errStr  sql.NullString
			filesJS sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.URL, &t.ChatTitle, &t.MessageID, &t.Kind,
			&group, &t.TotalSize, &t.DownloadedSize, &t.Speed, &t.Progress, &status,
			&t.SavePath, &t.CreatedAt, &t.FinishedAt, &errStr, &filesJS); err != nil {
			return nil, err
		}
		t.Status = task.Status(status)
		t.Group = group != 0
		t.Error = errStr.String
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
