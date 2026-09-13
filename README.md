# gocrawl

第一个 Go 项目：牛客网招聘帖/面经爬虫，支持并发抓取、增量爬取、SQLite 落盘。

**学习文档在 [LEARNING.md](./LEARNING.md)**——包含本项目的全部知识点讲解和启动方法，先读它。

## 功能

- 并发抓取：worker 池 + 动态任务队列，边爬边发现新链接
- HTML 解析：goquery 提取标题、正文、外链，页内去重
- 增量爬取：URL 落库去重，重复运行自动跳过已抓页面
- 礼貌爬取：请求限速、指数退避重试、UA 伪装、页数上限
- 数据落盘：SQLite（WAL 模式、参数绑定），可切换内存实现
- 优雅退出：Ctrl+C 触发 context 取消，worker 处理完手头任务再退出

## 目录结构

```
gocrawl/
├── main.go            入口：命令行参数、组装模块、Ctrl+C 优雅退出
├── config/            运行配置（并发数、抓取间隔、超时等）
├── fetcher/           HTTP 抓取：超时、指数退避重试、限速
├── parser/            goquery 解析：标题/正文/链接提取、页内去重
├── scheduler/         调度器 + worker 池：并发控制、动态队列
├── dedup/             URL 去重集合（mutex + map）
└── store/             存储接口 + 内存/SQLite 双实现
```

## 运行

```bash
go mod tidy
go run . -keyword "Go后端" -max 20
```

数据落盘到 `gocrawl.db`（SQLite），重复运行相同关键词走增量爬取。更多运行方式（含 `-race` 竞态检测）见 [LEARNING.md](./LEARNING.md) 第 10 节。

## 已知边界

牛客是 SPA，部分页面内容由 JavaScript 渲染，爬虫只能拿到原始 HTML 中的空壳（详见 LEARNING.md 第 8 节）。服务端渲染的页面（如搜索结果页）可完整抓取。
