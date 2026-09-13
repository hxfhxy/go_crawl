// Package scheduler 任务队列 + worker 池，是整个爬虫的心脏。
//
// 数据流：worker 从队列取任务 → fetcher 抓取 → parser 解析
// → store 保存 → 新链接作为新任务扔回队列，直到没有任务为止。
//
// 队列的生命周期用「pending 计数 + 专职关闭者」管理：
// 每入队一个任务 pending.Add(1)，处理完 pending.Done()；
// 一个独立 goroutine 在 pending 归零后 close 队列。
// 这样既支持「边爬边发现新任务」，又保证 close 一定发生在
// 所有生产者结束之后（向已关闭的 channel 发送会 panic）。
package scheduler

import (
	"context"
	"log"
	"net/url"
	"sync"

	"gocrawl/config"
	"gocrawl/dedup"
	"gocrawl/fetcher"
	"gocrawl/parser"
	"gocrawl/store"
)

// Task 是队列里的最小工作单元。
type Task struct {
	URL string
}

// Stats 汇总运行结果，最后打印给用户看。
type Stats struct {
	OKCount   int // 成功抓取并保存
	FailCount int // 抓取或解析失败
	SkipCount int // 重复 URL，跳过
}

type Scheduler struct {
	cfg     *config.Config
	fetcher *fetcher.Fetcher
	store   store.Store
	seen    *dedup.Set
	queue   chan Task
	pending sync.WaitGroup // 队列中尚未处理完的任务数
	wg      sync.WaitGroup // 存活的 worker 数
	stats   Stats
	statsMu sync.Mutex
}

func New(cfg *config.Config) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		fetcher: fetcher.New(cfg.Timeout, cfg.RequestDelay, cfg.MaxRetries),
		seen:    dedup.New(),
		queue:   make(chan Task, 1000),
	}
}

// Run 启动爬虫，阻塞直到任务全部完成或 ctx 被取消（如 Ctrl+C）。
func (s *Scheduler) Run(ctx context.Context) (*Stats, error) {
	st, err := store.New(s.cfg.StoreKind, s.cfg.DBPath)
	if err != nil {
		return nil, err
	}
	s.store = st
	defer st.Close()

	for _, seed := range s.cfg.Seeds {
		// 增量爬取第一步：种子也查库，爬过的直接跳过
		if seenDB, err := st.Seen(seed); err == nil && seenDB {
			s.bumpSkip()
			continue
		}
		s.seen.Add(seed)
		s.enqueue(ctx, Task{URL: seed})
	}

	// wg.Add 必须在 go 语句之前调用：放进 goroutine 内部会有竞态，
	// 主协程可能在计数加上去之前就通过了 Wait。
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	// 专职关闭者：所有任务处理完（pending 归零）后关闭队列，
	// worker 的 range/select 因此得知「不会再有新任务」，正常退出。
	// 注意 pending.Add 发生在处理当前任务的过程中（enqueueNew），
	// 早于当前任务的 Done，所以 Wait 不会提前返回。
	go func() {
		s.pending.Wait()
		close(s.queue)
	}()

	s.wg.Wait()
	return &s.stats, nil
}

func (s *Scheduler) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done(): // 收到退出信号：尽快收工
			return
		case task, ok := <-s.queue:
			if !ok { // 队列已关闭且取空：正常收工
				return
			}
			s.safeProcess(ctx, task)
		}
	}
}

// safeProcess 给 process 包上 panic 兜底和计数归还。
// 单个 goroutine 里未捕获的 panic 会让整个进程崩溃，
// 所以常驻服务的每个 worker 入口都应该有 recover。
func (s *Scheduler) safeProcess(ctx context.Context, task Task) {
	defer s.pending.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[panic] %s: %v", task.URL, r)
			s.bumpFail()
		}
	}()
	s.process(ctx, task)
}

func (s *Scheduler) process(ctx context.Context, task Task) {
	body, err := s.fetcher.Fetch(task.URL)
	if err != nil {
		log.Printf("[失败] %s: %v", task.URL, err)
		s.bumpFail()
		return
	}

	page, err := parser.Parse(task.URL, body)
	if err != nil {
		log.Printf("[解析失败] %s: %v", task.URL, err)
		s.bumpFail()
		return
	}

	if err := s.store.Save(page); err != nil {
		log.Printf("[存储失败] %s: %v", task.URL, err)
		s.bumpFail()
		return
	}
	s.bumpOK()
	log.Printf("[成功] %s (标题: %q, 链接: %d)", page.URL, page.Title, len(page.Links))

	s.enqueueNew(ctx, task.URL, page.Links)
}

// enqueue 把任务放入队列。pending.Add 必须在发送之前调用，
// 保证「关闭者」看到的计数永远覆盖所有在途任务。
func (s *Scheduler) enqueue(ctx context.Context, t Task) {
	if ctx.Err() != nil {
		return
	}
	s.pending.Add(1)
	select {
	case s.queue <- t:
	case <-ctx.Done(): // 退出时可能送不进去，把计数还回去
		s.pending.Done()
	}
}

func (s *Scheduler) enqueueNew(ctx context.Context, fromURL string, links []string) {
	base, err := url.Parse(fromURL)
	if err != nil {
		return
	}
	for _, link := range links {
		abs := parser.ResolveURL(base, link)
		if abs == "" {
			continue
		}
		if s.seen.Len() >= s.cfg.MaxPages { // 控制爬取规模
			return
		}
		if !s.seen.Add(abs) {
			s.bumpSkip()
			continue
		}
		// 增量爬取：内存去重之后还要查库——上次运行可能已经爬过它
		seenDB, err := s.store.Seen(abs)
		if err != nil {
			// 查询失败时宁可多爬一次，也不要中断整个流程
			log.Printf("[去重查询失败] %s: %v", abs, err)
		} else if seenDB {
			s.bumpSkip()
			continue
		}
		s.enqueue(ctx, Task{URL: abs})
	}
}

// 统计字段被所有 worker 并发读写，必须加锁；
// 连 s.stats.OKCount++ 都不是原子操作（读-加-写三步）。
func (s *Scheduler) bumpOK()   { s.statsMu.Lock(); s.stats.OKCount++; s.statsMu.Unlock() }
func (s *Scheduler) bumpFail() { s.statsMu.Lock(); s.stats.FailCount++; s.statsMu.Unlock() }
func (s *Scheduler) bumpSkip() { s.statsMu.Lock(); s.stats.SkipCount++; s.statsMu.Unlock() }
