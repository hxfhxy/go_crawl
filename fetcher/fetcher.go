// Package fetcher 负责发 HTTP 请求拿回页面内容。
// 职责边界：这个包只管「拿到字节」，不管怎么解析、怎么存储。
package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

type Fetcher struct {
	client     *http.Client
	delay      time.Duration
	maxRetries int
}

func New(timeout, delay time.Duration, maxRetries int) *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: timeout},
		delay:  delay,
		// 自定义浏览器 UA：Go 默认的 "Go-http-client/2.0" 一眼就能
		// 被识别为爬虫，很多网站会直接拒绝。
		maxRetries: maxRetries,
	}
}

// Fetch 抓取一个 URL，返回响应体字节。
// 内置限速（每次请求前 sleep delay）和指数退避重试。
func (f *Fetcher) Fetch(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避：1s, 2s, 4s... 避免对方刚拒绝你就立刻再撞上去
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		time.Sleep(f.delay)

		body, err := f.once(url)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("fetch %s: 重试 %d 次后仍失败: %w", url, f.maxRetries, lastErr)
}

func (f *Fetcher) once(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		// err 在这里捕获的是网络层故障：域名不存在、断网、握手超时等
		return nil, err
	}
	defer resp.Body.Close()

	// 4xx 是请求方的问题（如 URL 错了），重试无意义，直接报告为
	// 不可恢复错误；5xx 是对方临时故障，上层重试是有意义的。
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return nil, fmt.Errorf("客户端错误 %d（不可重试）", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("服务端错误 %d（可重试）", resp.StatusCode)
	}

	// resp.Body 是流式接口，网络数据一段段到达，
	// io.ReadAll 等待全部读完并拼成一个 []byte。
	return io.ReadAll(resp.Body)
}
