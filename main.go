// gocrawl 入口：解析命令行参数，组装各模块，处理 Ctrl+C 优雅退出。
//
// 运行示例：
//
//	go run . -keyword "Go后端" -max 20
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
)

func main() {
	log.SetFlags(log.Ltime)

	keyword := flag.String("keyword", "Go后端", "搜索关键词")
	maxPages := flag.Int("max", 20, "最多抓取的页面数")
	storeKind := flag.String("store", "sqlite", "存储类型: sqlite 或 memory")
	flag.Parse()

	cfg := config.Default()
	cfg.MaxPages = *maxPages
	cfg.StoreKind = *storeKind
	cfg.Seeds = []string{
		"https://www.nowcoder.com/search?query=" + url.QueryEscape(*keyword) + "&type=all",
	}

	// signal.NotifyContext 把 Ctrl+C / SIGTERM 转换成 ctx 取消。
	// 退出信号顺着 ctx 一路传给调度器和每个 worker，
	// worker 处理完手头任务后自然退出，Run 返回部分统计。
	// 任何一步都不应该 os.Exit 硬退——那会让内存里的数据全部丢失。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("开始爬取，关键词: %q, 页数上限: %d（Ctrl+C 可随时退出）", *keyword, cfg.MaxPages)

	s := scheduler.New(cfg)
	stats, err := s.Run(ctx)
	if err != nil {
		log.Fatalf("爬虫异常退出: %v", err)
	}

	fmt.Println("\n════════ 运行结果 ════════")
	fmt.Printf("成功: %d  失败: %d  跳过(重复): %d\n", stats.OKCount, stats.FailCount, stats.SkipCount)
	fmt.Printf("数据已保存到 %s（存储类型: %s），再次运行相同关键词会走增量爬取。\n", cfg.DBPath, cfg.StoreKind)
}
