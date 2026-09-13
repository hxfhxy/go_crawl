// Package store 定义存储接口并提供实现。
package store

import (
	"fmt"
	"sync"

	"gocrawl/parser"
)

// Store 是存储层的抽象。调度器只依赖这个接口而不是具体实现，
// 换数据库不需要动调度器的代码——面向接口编程。
type Store interface {
	// Save 保存一个页面
	Save(p *parser.Page) error
	// Seen 返回该 URL 是否已经存过（用于增量爬取）
	Seen(url string) (bool, error)
	Close() error
}

// MemoryStore：开箱即用的内存实现，重启数据就没了。
// map 被多个 worker 并发读写，需要锁保护。
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

func (m *MemoryStore) Close() error { return nil }

var _ Store = (*MemoryStore)(nil) // 编译期检查：MemoryStore 实现了 Store

// New 根据配置选择实现。
func New(kind, dbPath string) (Store, error) {
	switch kind {
	case "memory":
		return NewMemoryStore(), nil
	case "sqlite":
		return OpenSQLite(dbPath)
	default:
		return nil, fmt.Errorf("未知的存储类型: %s", kind)
	}
}
