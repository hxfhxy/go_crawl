// store/store.go
package store

import (
	"fmt"
	"sync"

	"gocrawl/parser"
)

// Store 定义页面持久化接口
type Store interface {
	Save(p *parser.Page) error
	Seen(url string) (bool, error)
	All() ([]*parser.Page, error) // 新增：读取所有已抓取页面
	Close() error
}

// MemoryStore 内存版存储（线程安全）
type MemoryStore struct {
	mu    sync.Mutex
	pages map[string]*parser.Page
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{pages: make(map[string]*parser.Page)}
}

func (m *MemoryStore) Save(p *parser.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages[p.URL] = p
	return nil
}

func (m *MemoryStore) Seen(url string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pages[url]
	return ok, nil
}

// All 返回内存中保存的所有页面切片副本
func (m *MemoryStore) All() ([]*parser.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pages := make([]*parser.Page, 0, len(m.pages))
	for _, p := range m.pages {
		pages = append(pages, p)
	}
	return pages, nil
}

func (m *MemoryStore) Close() error {
	return nil
}

var _ Store = (*MemoryStore)(nil)

func New(kind, dbPath string) (Store, error) {
	switch kind {
	case "memory":
		return NewMemoryStore(), nil
	case "sqlite":
		return OpenSQLite(dbPath)
	default:
		return nil, fmt.Errorf("未知存储引擎: %s", kind)
	}
}
