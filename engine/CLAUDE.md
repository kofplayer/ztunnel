# engine/ — 可复用引擎（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。与隧道业务无关的通用能力，分三块：`net`（网络框架，本项目核心）、`log`（文件日志）、`queue`（带长度上限的阻塞队列）。**零第三方依赖**。

| 子目录 | 职责 | 详细文档 |
|---|---|---|
| `net/` | 网络框架：连接/会话/编解码/中间件链 | [net/CLAUDE.md](net/CLAUDE.md)（L2） |
| `net/middleware/` | 中间件实现：分包/加密(type0/type1)/校验 | [net/middleware/CLAUDE.md](net/middleware/CLAUDE.md)（L2） |
| `log/` | 按小时轮转的文件日志 | 本文件下文 |
| `queue/` | 带长度上限的阻塞队列（环形缓冲实现） | 本文件下文 |

## log/ — 文件日志

- **用法**：`log.Main().Info(format, args...)`；入口处 `NewLog()` → `Init("./log", 前缀)` → `SetMainLog()`（见 [../cmd/CLAUDE.md](../cmd/CLAUDE.md)）。**必须检查 `Init` 的返回值**——两个入口都这么做，因为 `Init` 失败时 `logerAll` 为 nil。
- **文件名**：`<前缀>YYYYMMDDHH.log`，**按小时轮转**。判定方式是"比较该时刻对应的文件名与当前文件名"（`isNeedChangeFile`），因此**时钟回拨或夏令时回退也会正确切文件**（L-11 已修；旧实现逐级比较"年/月/日/时 旧<新"，只会朝前轮转）。
- **并发**：`Logger.DoLog`/`StartLog`/`EndLog` 全程持 `Logger.mu`；`FileLogger` 另有自己的 `mu`。修掉报告 H-1——原先整条链无锁，轮转时 A 置 `logger=nil` 并关文件、B 正在读同一字段（数据竞争），且 A/B 同时判定需轮转会各开一个文件、后写者覆盖前者 → **前一个 `*os.File` 及其 fd 永久泄漏**。
- **错误传播**：`FileLogger.DoLog` 返回 `Output` 的错误、`closeLogger` 返回关闭错误（L-10，旧实现全部丢弃，磁盘满时日志静默消失）；`EndLog` 之后再写返回错误而非 nil panic；`EndLog` 幂等。
- **不复活**：`DoLog` 见 `logger == nil` 直接报错，**不会**自动重开文件——否则 `Uninit` 之后一次偶然写入就把日志文件悄悄重新打开。`logImp.doLog` 在 `logerAll == nil` 时降级写 stderr 并返回 error，绝不 panic（`recoverPanic` 兜底路径也要写日志，兜底的兜底 panic 等于杀死进程）。
- **级别**：常量 `DEBUG=0 ~ NONE=5`，`SetLogLevel` 过滤；`level > DEBUG` 时同时打印到 stdout。
- **行号**：`runtime.Caller(2)` 取调用方文件:行号写入日志。
- 结构：`logImp`（实现 `Log` 接口）→ `Logger`（轮转策略）→ `FileLogger`（实现 `ILogger` 接口，直接 `log.Logger.Output` 同步写文件，无缓冲 channel——注释里留有异步方案）。
- ⚠️ **仍存在的缺口**：`Fatal` 级别既不 `os.Exit` 也不与其它级别区别对待；日志**无大小上限、无自动清理**（只按小时切文件名），长期运行有撑爆磁盘的风险；`SetLogLevel` 用 `atomic.StoreInt32` 但 `Debug/Info/...` 读字段时非原子（宽松场景可接受，但与上面加锁改造不一致，属遗留）。

## queue/ — 带长度上限的阻塞队列

- **接口** `queueDef.Queue`：`Enqueue/Dequeue/Close/IsClose`；**唯一入口** `queue.NewQueue(buffLen)`（当前固定返回 ring 实现，Init 失败会 panic）。
- **实现** `imp/ring/`：`QQueue`（适配器）→ `Queue[T]`（泛型，`sync.Mutex` + `sync.Cond` 实现阻塞收发，关闭后 `Dequeue` 返回 `(_, false)`）→ `RingBuffer[T]`（非线程安全环形缓冲，**满了翻倍扩容**，Pop 时清引用助 GC）。
- **用途**：仅被 [net/connect/socket/conn.go](net/CLAUDE.md) 使用——每连接的发送缓冲（初始容量 32、默认上限 `DefaultSendQueueLimit`），`SendData` 入队正常路径下不阻塞、`senderRun` goroutine 出队写 socket。
- 注意：`Queue.Close` 后 `Enqueue` 返回哨兵 **`ErrClosed`**；积压达到上限返回 **`ErrFull`**（两者均可用 `errors.Is` 区分，报告 M-17/P-04）；`Dequeue` 在排空且已关闭后返回 `false`——调用方以此感知连接关闭。上限判定与入队在同一次加锁内完成（`SendLimited`），不提供"先 `Len()` 再 `Send()`"的写法。
