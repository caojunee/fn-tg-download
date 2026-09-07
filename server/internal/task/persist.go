package task

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Persister 任务持久化接口。
// Save 为全量快照语义（upsert 全部任务）；JSON 实现重写文件，MySQL 实现逐条 upsert。
type Persister interface {
	Load() ([]*Task, error)
	Save(tasks []*Task) error
	// Delete 删除指定任务记录（调用方保证均为非活跃任务）。
	Delete(ids []string) error
	// DeleteFinished 删除所有非活跃（历史）任务记录。
	DeleteFinished() error
}

// NewJSONPersister 返回基于单文件的持久化（原实现，作为无库环境的兜底）。
func NewJSONPersister(dataDir string) Persister {
	return &jsonPersister{dataDir: dataDir}
}

type jsonPersister struct {
	dataDir string
}

func (p *jsonPersister) path() string { return filepath.Join(p.dataDir, "tasks.json") }

func (p *jsonPersister) Load() ([]*Task, error) {
	b, err := os.ReadFile(p.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []*Task
	if err := json.Unmarshal(b, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (p *jsonPersister) Save(tasks []*Task) error {
	b, err := json.MarshalIndent(tasks, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.path(), b, 0o644)
}

// Delete/DeleteFinished 在全量快照语义下是空操作：Save 已重写整个文件。

func (p *jsonPersister) Delete([]string) error { return nil }

func (p *jsonPersister) DeleteFinished() error { return nil }

// URLEntry 链接台账项：一条 URL 最近一次任务的快照（持久化于 download_urls 表，
// 不随清历史删除），用于解析链接时提示"已下载过"。
type URLEntry struct {
	URL        string `json:"url"`
	Status     string `json:"status"`
	Name       string `json:"name"`
	FinishedAt int64  `json:"finishedAt"`
}

// urlLedger 支持链接台账的持久化实现（MySQL）；JSON 兜底实现不支持，返回 false。
type urlLedger interface {
	LookupURLs(urls []string) (map[string]URLEntry, error)
}
