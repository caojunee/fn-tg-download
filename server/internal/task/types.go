// Package task 管理下载任务的生命周期。
package task

type Status string

const (
	StatusPending     Status = "pending"
	StatusDownloading Status = "downloading"
	StatusPaused      Status = "paused"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCanceled    Status = "canceled"
)

func (s Status) Active() bool {
	return s == StatusPending || s == StatusDownloading || s == StatusPaused
}

// File 任务内的单文件信息。
type File struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Downloaded int64   `json:"downloaded"`
	Progress   float64 `json:"progress"`
	Status     Status  `json:"status"`
}

// Task 与前端 DownloadTask 结构对齐。
type Task struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	ChatTitle      string  `json:"chatTitle"`
	MessageID      int64   `json:"messageId"`
	Kind           string  `json:"kind"` // single | album | group
	Group          bool    `json:"group"`
	TotalSize      int64   `json:"totalSize"`
	DownloadedSize int64   `json:"downloadedSize"`
	Speed          int64   `json:"speed"`
	Progress       float64 `json:"progress"`
	Status         Status  `json:"status"`
	SavePath       string  `json:"savePath"`
	CreatedAt      int64   `json:"createdAt"`
	FinishedAt     int64   `json:"finishedAt,omitempty"`
	Error          string  `json:"error,omitempty"`
	Files          []File  `json:"files"`
}

func (t *Task) clone() Task { c := *t; return c }
