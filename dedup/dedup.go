package dedup

import "sync"

type Set struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string // 记录插入顺序，方便调试时打印
}

func New() *Set {
	return &Set{seen: make(map[string]struct{})}
}

// Add 把 url 加入集合。返回 true 表示这是新 URL；
// 返回 false 表示之前已经见过了，调用方应当跳过。
func (s *Set) Add(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[url]; ok {
		return false
	}
	s.seen[url] = struct{}{}
	s.order = append(s.order, url)
	return true
}

// Len 返回目前见过的 URL 总数。
func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}
