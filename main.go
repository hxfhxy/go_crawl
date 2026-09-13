package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"gocrawl/config"
	"gocrawl/scheduler"
	"gocrawl/stats" // 引入 stats 包
	"gocrawl/store"
)

func main() {
	log.SetFlags(log.Ltime)

	keyword := flag.String("keyword", "Go后端", "搜索关键词")
	maxPages := flag.Int("max", 20, "最大抓取页面数")
	storeKind := flag.String("store", "sqlite", "存储引擎: sqlite 或 memory")
	flag.Parse()

	cfg := config.Default()
	cfg.MaxPages = *maxPages
	cfg.StoreKind = *storeKind
	cfg.Seeds = []string{
		"https://www.nowcoder.com/search?query=" + url.QueryEscape(*keyword) + "&type=all",
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("爬虫启动: 关键词 %q, 目标抓取数 %d (按 Ctrl+C 可中断)", *keyword, cfg.MaxPages)

	s := scheduler.New(cfg)
	// 将变量重命名为 crawlStats，防止遮蔽 stats 包名
	crawlStats, err := s.Run(ctx)
	if err != nil {
		log.Fatalf("运行失败: %v", err)
	}

	fmt.Println("\n抓取完成:")
	fmt.Printf("成功: %d  失败: %d  跳过(去重): %d\n", crawlStats.OKCount, crawlStats.FailCount, crawlStats.SkipCount)
	fmt.Printf("数据保存在: %s (引擎: %s)\n", cfg.DBPath, cfg.StoreKind)

	// 打开存储并执行词频分析与展示
	st, err := store.New(cfg.StoreKind, cfg.DBPath)
	if err != nil {
		log.Printf("打开存储读取统计失败: %v", err)
		return
	}
	defer st.Close()

	freqs, err := stats.Analyze(st)
	if err != nil {
		log.Printf("词频统计失败: %v", err)
		return
	}
	stats.Print(freqs)
}
