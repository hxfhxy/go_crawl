// Package config 集中存放运行配置，避免一些固定常数散落在各个模块里。
package config

import "time"

type Config struct {
	// 起始抓取的种子页面
	Seeds []string

	// 并发 worker 数量。对个人学习爬虫，5 就足够了；
	// 调大不会让你更快爬到东西，只会更容易被对方封禁。
	Workers int

	// 相邻两次请求之间的间隔，对目标站点保持礼貌
	RequestDelay time.Duration

	// 单次 HTTP 请求超时
	Timeout time.Duration

	// 请求失败后的重试次数
	MaxRetries int

	// 最多处理的页面数，防止失控
	MaxPages int

	// 数据库文件路径
	DBPath string

	// 存储实现类型：sqlite 或 memory
	StoreKind string
}

func Default() *Config {
	return &Config{
		Seeds:        []string{"https://www.nowcoder.com/search?query=Go%E5%90%8E%E7%AB%AF&type=all"}, // 默认种子是牛客网搜索 Go 后端的结果页
		Workers:      5,                                                                               // 默认 5 个并发 worker
		RequestDelay: 1500 * time.Millisecond,                                                         // 默认相邻请求间隔 1.5 秒
		Timeout:      10 * time.Second,                                                                // 默认单次请求超时 10 秒
		MaxRetries:   3,                                                                               // 默认失败重试 3 次
		MaxPages:     20,                                                                              // 默认最多抓取 20 个页面
		DBPath:       "gocrawl.db",                                                                    // SQLite 数据文件
		StoreKind:    "sqlite",                                                                        // 存储实现：sqlite 或 memory
	}
}
