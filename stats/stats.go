// Package stats 分析已抓取网页中的技术关键词频次
package stats

import (
	"fmt"
	"sort"
	"strings"

	"gocrawl/store"
)

var keywords = []string{
	// 存储 / 缓存 / 中间件
	"mysql", "redis", "mongodb", "postgresql", "elasticsearch", "clickhouse",
	"kafka", "rabbitmq", "rocketmq",
	// Go 生态
	"golang", "gin", "gorm", "beego", "goroutine", "channel", "websocket",
	// 微服务 / 通信
	"grpc", "protobuf", "microservice", "microservices", "etcd", "nginx",
	// 云原生 / 基础架构
	"docker", "kubernetes", "k8s", "prometheus", "raft", "cron",
	// 计算机基础
	"linux", "tcp", "http", "git", "sql", "api",
}

// Frequency 封装关键词与对应出现次数
type Frequency struct {
	Word  string
	Count int
}

// Analyze 读取所有页面文本并统计关键词频次，按次数降序排列
func Analyze(st store.Store) ([]Frequency, error) {
	pages, err := st.All()
	if err != nil {
		return nil, fmt.Errorf("读取页面记录失败: %w", err)
	}

	// 1. 词频累加：统一转小写，避免大小写重复统计（如 Redis vs redis）
	counts := make(map[string]int)
	for _, p := range pages {
		lowerText := strings.ToLower(p.Text)
		for _, kw := range keywords {
			c := strings.Count(lowerText, kw)
			if c > 0 {
				counts[kw] += c
			}
		}
	}

	// 2. 收集频次 > 0 的关键词
	freqs := make([]Frequency, 0, len(counts))
	for kw, count := range counts {
		freqs = append(freqs, Frequency{
			Word:  kw,
			Count: count,
		})
	}

	// 3. 排序：按 Count 降序；若频次相同则按字典序升序
	sort.Slice(freqs, func(i, j int) bool {
		if freqs[i].Count == freqs[j].Count {
			return freqs[i].Word < freqs[j].Word
		}
		return freqs[i].Count > freqs[j].Count
	})

	return freqs, nil
}

// Print 输出终端柱状词频图
func Print(freqs []Frequency) {
	fmt.Println("\n===== 技术关键词词频统计 TOP =====")
	if len(freqs) == 0 {
		fmt.Println("（暂无统计数据，请确认是否抓取到有效文本）")
		return
	}

	// 计算最长关键词长度，保证左侧对齐
	maxLen := 0
	for _, f := range freqs {
		if l := len(f.Word); l > maxLen {
			maxLen = l
		}
	}

	for _, f := range freqs {
		barLen := f.Count
		if barLen > 50 { // 避免超高频词导致终端换行失真
			barLen = 50
		}
		bar := strings.Repeat("█", barLen)
		fmt.Printf("%*s  %3d  %s\n", maxLen, f.Word, f.Count, bar)
	}
}
