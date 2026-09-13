// Package parser 提供 HTML 解析与结构化数据提取
package parser

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Page 封装抓取出的结构化数据
type Page struct {
	URL   string   // 页面 URL
	Title string   // <title> 内容
	Text  string   // 正文纯文本
	Links []string // 页面内的合法外链 URL
}

// Parse 使用 goquery 解析 HTML 响应体，提取 Title、Text 和 Links
func Parse(rawURL string, body []byte) (*Page, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	p := &Page{URL: rawURL}

	// 1. 提取网页标题
	p.Title = strings.TrimSpace(doc.Find("title").First().Text())

	// 2. 提取并去重所有 <a> 标签的合法跳转链接
	// 注意：必须在清理 DOM 树之前提取链接，避免误删导航栏中的有效入口
	linkMap := make(map[string]struct{})
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}
		abs := ResolveURL(base, href)
		if abs == "" {
			return
		}
		// 页面内部排重，避免向调度器塞入大量同名重复链接
		if _, seen := linkMap[abs]; !seen {
			linkMap[abs] = struct{}{}
			p.Links = append(p.Links, abs)
		}
	})

	// 3. 移除噪音 DOM 节点（脚本、样式、内联框架及纯视觉标签）
	doc.Find("script, style, noscript, svg, iframe").Remove()

	// 4. 提取 Body 纯文本并压缩多余空白字符
	// strings.Fields 能自动合并连续换行、制表符与空格，大幅缩减 SQLite 存储体积
	rawText := doc.Find("body").Text()
	p.Text = strings.Join(strings.Fields(rawText), " ")

	return p, nil
}

// ResolveURL 解析相对路径并过滤非法协议
func ResolveURL(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "#") {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)

	// 仅收录 http 与 https 协议，过滤 mailto:、ftp:、tel: 等
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}

	// 统一去除 URL 锚点（如 /index#header 与 /index 代表同一个页面实体）
	abs.Fragment = ""
	return abs.String()
}
