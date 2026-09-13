# gocrawl 学习笔记

这份文档把热身爬虫项目里所有知识点集中在一起：并发模型、网络细节、错误处理、语法点和工程习惯。读完代码再读文档，或者读完文档再回头读代码，都行。

---

## 1. 项目全貌

**目标**：爬取牛客网上「Go 后端」相关的招聘帖/面经，存下来并做技能关键词词频统计——产出自 jd 分析，直接服务求职。

**数据流**（一个任务在系统里的完整旅程）：

```
main（构造种子 URL）
  → Scheduler.Run（入队、启动 worker 池）
    → worker 从队列取任务
      → fetcher：发 HTTP 请求拿到 HTML 字节（限速 + 重试）
      → parser：解析出标题/正文/链接
      → store：保存页面（SQLite）
      → 新链接作为新任务扔回队列（循环，直到没有任务）
  → 全部完成或 Ctrl+C，打印统计
```

**目录职责**——每个包只做一件事：

| 包 | 职责 | 依赖 |
|---|---|---|
| `config` | 集中放运行参数 | 无 |
| `fetcher` | 拿字节 | 标准库 |
| `parser` | 字节 → 结构化页面 | goquery |
| `store` | 页面落盘 | 接口 + 内存/SQLite 实现 |
| `dedup` | URL 去重集合 | 无 |
| `scheduler` | 并发调度（心脏） | 上面所有包 |
| `main` | 组装、命令行、退出信号 | config + scheduler |

改解析逻辑不会碰网络代码，换数据库不用改调度器——这就是分包的意义。

---

## 2. 并发核心（本项目最重要的部分）

### 2.1 goroutine 与 worker 池

goroutine 是 Go 的轻量级协程，`go f()` 一行就启动，初始栈只有几 KB。但「能开一万个」不代表「该开一万个」：爬虫的并发度必须可控，否则等于对目标网站发起攻击，对方一个封号请求你就出局了。

所以用 **worker 池**：固定开 `cfg.Workers` 个 goroutine，从一个共享队列里领任务。并发度被池子钉死，这是所有后端服务控制下游压力的基本思路（数据库连接池、线程池都是同一思想的变体）。

### 2.2 WaitGroup：「等所有人都干完」

```go
for i := 0; i < s.cfg.Workers; i++ {
    s.wg.Add(1)      // 计数 +1
    go s.worker(ctx) // 启动
}
// ...
s.wg.Wait()          // 阻塞，直到计数归零
```

worker 里第一行是 `defer s.wg.Done()`（计数 -1）。

三个必须知道的细节：

- **`Add` 必须在 `go` 语句之前调用**。如果放进 goroutine 内部，主协程可能在新计数加上去之前就通过了 `Wait`——真实项目里出现过的经典竞态。
- `Done` 写在 `defer` 里，保证任何 return 路径（包括 panic 展开栈的时候）都会执行。
- 概念上：**WaitGroup 解决「自下而上的等待」**（主协程等所有子协程完成），**channel / context 解决「自上而下的通知」**（主协程向下游广播退出信号）。两者不是竞争关系，是配合关系。

本项目用了**两个** WaitGroup，分工不同：`wg` 数 worker 的存活数，`pending` 数队列里在途任务的数量（见 2.4）。

### 2.3 channel：三条规则 + range

channel 是 goroutine 之间传递数据的管道。本项目里的 `queue chan Task` 容量 1000（带缓冲：缓冲满之前发送方不阻塞）。

**三条铁律**：

1. 向 `nil` channel 发送/接收 → 永久阻塞
2. 向**已关闭**的 channel 发送 → **panic**（这是本项目的经典陷阱）
3. 从已关闭的 channel 接收 → 先取完缓冲里剩下的值，然后立刻返回零值

所以 `close(q)` 的语义是「生产者向全体消费者承诺：不会再写了」。谁来 close？**只有生产者一方可以**。消费者 close 队列是并发 bug 的头号来源。

消费端的惯用写法：

```go
for task := range s.queue { ... }
// channel 被 close 且取空后，range 循环自动结束
```

`for range channel` 会阻塞等值、取值、取完自动退出，比手动 `v, ok := <-q` 判断更干净。

**为什么用 channel 当队列而不用「slice + 锁」？** slice 加 Mutex 也能实现线程安全队列，但空队列时 worker 只能不停加锁轮询（浪费 CPU），或者引入很难调的 `sync.Cond`。channel 的阻塞和唤醒由 Go 运行时调度器直接实现：队满发送者休眠、队空接收者休眠，无任务时 CPU 占用为零。这是 Go「通过通信共享内存」哲学的直接体现。

### 2.4 动态任务队列：本项目的关键设计

爬虫有个队列设计上的难题：任务不是一次性给定的，而是**边爬边发现**的（页面上挖到新链接）。这就意味着：

- 不能在主协程里 `close(queue)`——主协程根本不是唯一的生产者，worker 随时可能挖出新链接要入队；
- 但队列也永远不能不关——不然 worker 的 range 永远等下去。

本项目采用的业界标准解法：**pending 计数 + 专职关闭者**。

```go
// 入队（生产者）：
s.pending.Add(1)          // 先加计数
select {
case s.queue <- t:        // 再发送
case <-ctx.Done():
    s.pending.Done()      // 没送进去，把计数还回去
}

// worker 消费完一个任务：
defer s.pending.Done()    // 处理完，计数 -1

// 专职关闭者（唯一的 close 点）：
go func() {
    s.pending.Wait()      // 所有任务处理完
    close(s.queue)        // 此刻再关，绝不 panic
}()
```

**为什么它是对的**：新任务在 `process` 过程中被入队（`enqueueNew`），此时当前任务的 `Done` 还没执行（在 defer 里），所以 pending 永远不会在「还有活没干」时归零——关闭者不会提前 close。这是无锁的、只靠两个原语组合就正确的设计，比「给 close 加把锁」优雅得多。

### 2.5 select：一个 goroutine 同时等多件事

worker 的主循环用了 select 同时等两个事件：

```go
select {
case <-ctx.Done():        // 退出信号到了
    return
case task, ok := <-s.queue: // 新任务到了
    if !ok { return }     // 队列关闭且取空，正常收工
    s.safeProcess(ctx, task)
}
```

select 语义：多个 case 都就绪时**随机**选一个执行（避免饥饿），都不就绪则阻塞。它是 Go 里「多路等待」的唯一原语，超时控制（`time.After`）、退出通知（`ctx.Done()`）都是靠它织进主逻辑的。

### 2.6 互斥锁与数据竞争

两个 goroutine 同时读写普通变量 = 数据竞争，后果分两档：

- **map 并发读写：直接 panic**，整个程序崩。Go 运行时故意这么做，逼你修。（`go run` 加 `-race` 参数可以精确检测出所有数据竞争，学会用它。）
- **`i++` 也不是原子的**：它是「读 → 加一 → 写回」三步，两个 goroutine 交错执行就会丢更新。本项目 `stats.OKCount++` 也老老实实加了 `statsMu`。

锁的惯用法：`Lock` 之后立刻 `defer Unlock`，任何 return 路径都不会忘记解锁。

### 2.7 panic 与 recover：Go 的崩溃模型

Go 的规则很残酷：**任何一个 goroutine 里未捕获的 panic，会让整个进程崩溃**，其他所有 goroutine 被一起拖死（不像某些语言里异常只影响当前线程）。panic 展开栈时确实会执行调用链上的 defer，但那只是陪你死。

所以常驻服务的每个 worker 入口都要兜底：

```go
defer func() {
    if r := recover(); r != nil {
        log.Printf("[panic] %s: %v", task.URL, r)
        s.bumpFail()
    }
}()
```

一个页面解析炸了，只损失这一个任务，其他 worker 继续干活。注意 recover 必须写在 **deferred 函数**里才生效。

### 2.8 context：退出信号怎么传导

`main.go` 里一行代码接住了 Ctrl+C：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```

之后 ctx 被原样传进 `Run(ctx)` → `worker(ctx)` → `process(ctx)`。整条传导链是：

```
你按下 Ctrl+C
  → OS 发 SIGINT
  → signal.NotifyContext 把它变成 ctx 取消
  → worker 的 select 感知到 ctx.Done()，处理完手头任务后退出
  → Run 的 wg.Wait() 返回，打印统计，main 正常结束
```

**为什么不直接 `os.Exit(0)`？** 硬退出不给任何收尾机会：内存里的数据全丢、defer 不执行、正在写的文件可能残缺。K8s 滚动更新时杀容器就是发 SIGTERM，没有优雅退出的服务会在生产环境反复踩这个坑。

---

## 3. 网络与 HTTP 细节（fetcher）

### 3.1 构造一个像样的请求

```go
req, err := http.NewRequest(http.MethodGet, url, nil) // 组装报文对象
req.Header.Set("User-Agent", "Mozilla/5.0 ... Chrome/124.0 ...")
resp, err := f.client.Do(req)
```

- `NewRequest` 第三参是请求体，GET 没有 body，传 nil。
- **User-Agent（UA）**是 HTTP 头里「我是谁」的字段。Go 默认 UA 是 `Go-http-client/2.0`，一眼爬虫，很多网站直接拒。伪装成常见浏览器能绕过第一道拦截。
- `http.Client{Timeout: 10s}` 是**整请求级超时**（连接 + 发送 + 读响应全覆盖）。永远不要用无超时的 client——一个卡死的连接就能拖死一个 worker。

### 3.2 重试的分类学与指数退避

不是所有错误都值得重试：

- **4xx**（如 400/404）：请求方自己的问题，重试一万次还是 400 → 不重试，直接报错。
- **5xx**：对方临时故障 → 重试有意义。
- **网络层错误**（断网、超时）→ 重试。

重试的节奏用**指数退避**：1s、2s、4s……（`1<<(attempt-1) * time.Second`，位运算 `1<<n` 就是 2ⁿ）。对方刚拒绝你就原速再撞，只会被封。

### 3.3 响应体是流

`resp.Body` 是 `io.Reader`——网络数据一段段到达，`io.ReadAll` 阻塞到全部读完拼成 `[]byte`。`defer resp.Body.Close()` 一样不能少，不关会泄漏连接（底层是文件描述符）。

---

## 4. 错误处理姿势

Go 没有 try/catch，**错误就是普通的返回值**，`if err != nil` 是这个语言的心跳。要点：

- **错误包装**：`fmt.Errorf("fetch %s: ...: %w", url, lastErr)`。`%w` 把底层错误包进去，信息不丢，调用方还能用 `errors.Is / errors.As` 解包判断。每一层报错都补上自己的上下文（这里是 URL），最后打印出来就是一条完整链路。
- **区分可恢复与不可恢复**：见 3.2。「无脑重试」和「一错就崩」之间，专业做法是分类。
- 每个失败路径都调用 `bumpFail()` 计数再 return——错误被记录而不是被吞掉。

---

## 5. 接口与工程结构

### 5.1 面向接口（store 包）

```go
type Store interface {
    Save(p *parser.Page) error
    Seen(url string) (bool, error)
    Close() error
}
```

调度器只认 `Store` 接口。现在 `store.New` 里有两个实现：`MemoryStore` 和 `SQLiteStore`，通过命令行 `-store sqlite|memory` 切换，**调度器一行都不用改**。这个模式叫依赖倒置，是 Go 工程里最重要的解耦手段；单元测试时塞一个假实现进去，就能不连真数据库地测调度逻辑。

### 5.2 编译期接口检查

```go
var _ Store = (*MemoryStore)(nil)
```

声明一个丢弃的接口类型变量，如果 `MemoryStore` 没实现全接口，**编译直接报错**而不是等到运行时。Go 惯用法，看到就认识。

### 5.3 其他工程习惯

- `gofmt -w .`：格式统一，团队协作底线，保存前跑一下。
- `go vet ./...`：静态检查，能抓出格式化参数不匹配、不可达代码等问题。
- `go mod tidy`：按 import 自动增删 `go.mod` 里的依赖。
- 魔法数字集中进 `config`，不散落在业务代码里。

---

## 6. 语法点速查

| 语法 | 项目里的例子 | 说明 |
|---|---|---|
| 指针接收者 | `func (f *Fetcher) Fetch(...)` | 方法要修改接收者状态、或结构体较大时用指针；值接收者每次拿到副本 |
| 多返回值 | `Fetch(url) ([]byte, error)` | Go 没有 try/catch，错误作为最后一个返回值显式传递 |
| defer | `defer resp.Body.Close()` | 注册在函数返回时执行，多个 defer 后进先出（LIFO）；panic 展开栈时也会执行 |
| 闭包 | `go func() { s.pending.Wait(); close(s.queue) }()` | 匿名函数捕获外部变量，goroutine 启动的标准姿势 |
| 结构体复合字面量 | `Scheduler{cfg: cfg, queue: make(chan Task, 1000)}` | 带字段名的初始化，字段顺序无关 |
| `var _ I = (*T)(nil)` | store 包末尾 | 编译期检查 T 实现了接口 I |
| 位运算 | `1 << (attempt-1)` | `1<<n` 即 2ⁿ，用于指数退避 |
| 空结构体 | `seen map[string]struct{}` | `struct{}` 占 0 字节，表达「只要存在性不要值」的集合 |
| `flag` 包 | `flag.String("keyword", ...)` | 标准库命令行参数解析，`-keyword "xx"` |
| `url.QueryEscape` | main.go 拼种子 URL | 中文和特殊字符必须转义后才能放进 URL 查询串，否则对方 400 |
| 相对路径补全 | `parser.ResolveURL` | `(*url.URL).ResolveReference` 把 `/post/1` 补成完整 URL；锚点 `#xxx` 去重（同一页面） |
| `go run` / `go build` / `go get` / `go mod tidy` | — | 运行 / 编译二进制 / 添加依赖 / 整理依赖 |

---

## 7. 礼貌爬取的细节清单

个人学习爬虫的边界，每一条都在代码里有对应实现：

- **限速**：每次请求前 `sleep 1.5s`（`config.RequestDelay`）。
- **并发上限**：worker 池 5 个。
- **总量上限**：`-max 20`，`enqueueNew` 里见到 `MaxPages` 就停。
- **去重**：`dedup.Set`，同一 URL 永不爬两次。
- **伪装 UA**：见 3.1。
- **只爬公开页面**：不碰登录态、不绕风控，数据自用。

---

## 8. 解析层：goquery 与 SPA 的边界

parser 是把 HTML 字节变成结构化数据的最后一环，实现要点和认知边界都值得记录。

### 实现要点（`parser/parser.go`）

- **goquery 的语法就是 CSS 选择器**：`doc.Find("title")`、`doc.Find("a[href]")`，会浏览器的 `querySelector` 就会它，底层是 `golang.org/x/net/html` 解析出的 DOM 树。
- **先提取链接，再删噪音节点**：`script/style/noscript/svg/iframe` 的 `Remove()` 必须放在链接提取**之后**，否则导航栏里被这些标签包裹的入口链接会一起丢失。顺序敏感的操作，注释里要写清为什么。
- **页内排重**：同一个链接在页面上出现 N 次很常见，用 `map[string]struct{}` 在页面内部先去重，不给调度器塞重复任务。
- **正文压缩**：`strings.Fields` 自动按连续空白切分、`strings.Join` 拼回单空格，正文存储体积大幅缩小。

### SPA 边界：知道抓不到什么

牛客是单页应用（SPA）：搜索结果页走服务端渲染，爬虫能拿到完整正文；但不少页面的内容由浏览器执行 JavaScript 后才渲染，原始 HTML 里只有一个空壳——这正是库里某些记录正文只有几十字节的原因。

解决办法是无头浏览器（chromedp / playwright），成本高一个数量级，不值得在个人爬虫里做。**能说清自己的工具抓不到什么、为什么，比假装全能更专业**——面试聊到爬虫时这是一等一的加分话题。

---

## 9. 常见坑速查

| 坑 | 后果 | 出处 |
|---|---|---|
| `wg.Add` 放在 `go` 里面 | Wait 提前通过 | 2.2 |
| 向已关闭的 channel 发送 | panic | 2.3 / 2.4 |
| map 并发读写 | 直接崩溃 | 2.6 |
| worker 无 recover | 一个 panic 全进程死 | 2.7 |
| Ctrl+C 里 `os.Exit` | 数据全丢、defer 不执行 | 2.8 |
| 无超时的 http.Client | 一个卡死连接拖死 worker | 3.1 |
| URL 里的中文没转义 | 对方返回 400 | 6 |
| 相对链接没补全 | `dedup` 认为 `/a` 和 `https://x.com/a` 不同 | 6 |

---

## 10. 启动方法

环境要求：Go 1.21+（`go version` 检查）。

```bash
cd /home/hzy/gocrawl

# 第一次运行前：整理依赖
go mod tidy

# 基本用法：关键词 + 页数上限（数据落盘到 gocrawl.db）
go run . -keyword "Go后端" -max 20

# 换个关键词
go run . -keyword "golang 校招" -max 50

# 临时不落盘（纯内存，重启即失）
go run . -keyword "Go后端" -max 5 -store memory

# 编译成二进制再运行
go build -o gocrawl .
./gocrawl -keyword "Go后端" -max 20

# 检测数据竞争（学习并发时建议经常跑）
go run -race . -keyword "Go后端" -max 5

# 中途退出：Ctrl+C，会打印已完成的统计；重复运行相同关键词走增量爬取
```

参数说明：`-keyword` 搜索关键词、`-max` 页数上限、`-store` 存储类型（默认 `sqlite`，落盘到 `gocrawl.db`）。

提交代码前先跑 `go mod tidy`，保证 `go.mod`/`go.sum` 与 import 一致——否则别人 clone 下来编译不过。

输出说明：日志里 `[成功] URL (标题: "...", 链接: N)` 表示一个页面处理完成；结束时会打印 `成功 / 失败 / 跳过` 统计。数据落盘到 `gocrawl.db`，重复运行相同关键词走增量爬取。
