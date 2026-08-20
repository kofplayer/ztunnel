# engine/ — 可复用引擎（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。与隧道业务无关的通用能力，分三块：`net`（网络框架，本项目核心）、`log`（文件日志）、`queue`（无界队列）。**零第三方依赖**。

| 子目录 | 职责 | 详细文档 |
|---|---|---|
| `net/` | 网络框架：连接/会话/编解码/中间件链 | [net/CLAUDE.md](net/CLAUDE.md)（L2） |
| `net/middleware/` | 中间件实现：分包/加密(type0/type1)/校验 | [net/middleware/CLAUDE.md](net/middleware/CLAUDE.md)（L2） |
| `log/` | 按小时轮转的文件日志 | 本文件下文 |
| `queue/` | 无界阻塞队列（环形缓冲实现） | 本文件下文 |

## log/ — 文件日志

- **用法**：`log.Main().Info(format, args...)`；入口处 `NewLog()` → `Init("./log", 前缀)` → `SetMainLog()`（见 [../cmd/CLAUDE.md](../cmd/CLAUDE.md)）。
- **文件名**：`<前缀>YYYYMMDDHH.log`，**按小时轮转**（`isNeedChangeFile` 逐级比较年月日时），`Init` 时自动建目录。
- **级别**：常量 `DEBUG=0 ~ NONE=5`，`SetLogLevel` 过滤；`level > DEBUG` 时同时打印到 stdout。
- **行号**：`runtime.Caller(2)` 取调用方文件:行号写入日志。
- 结构：`logImp`（实现 `Log` 接口）→ `Logger`（轮转策略）→ `FileLogger`（实现 `ILogger` 接口，直接 `log.Logger.Output` 同步写文件，无缓冲 channel——注释里留有异步方案）。
- ⚠️ `SetLogLevel` 用 `atomic.StoreInt32` 但 `Debug/Info/...` 读取时直接读字段，非原子（宽松场景可接受）。

## queue/ — 无界阻塞队列

- **接口** `queueDef.Queue`：`Enqueue/Dequeue/Close/IsClose`；**唯一入口** `queue.NewQueue(buffLen)`（当前固定返回 ring 实现，Init 失败会 panic）。
- **实现** `imp/ring/`：`QQueue`（适配器）→ `Queue[T]`（泛型，`sync.Mutex` + `sync.Cond` 实现阻塞收发，关闭后 `Dequeue` 返回 `(_, false)`）→ `RingBuffer[T]`（非线程安全环形缓冲，**满了翻倍扩容**，Pop 时清引用助 GC）。
- **用途**：仅被 [net/connect/socket/conn.go](net/CLAUDE.md) 使用——每连接的发送缓冲（初始容量 32），`SendData` 入队永不阻塞、`senderRun` goroutine 出队写 socket。语义近似「无缓冲上限的 channel」。
- 注意：`Queue.Close` 后 `Enqueue` 返回 error（"closed"），`Dequeue` 在排空后返回 false——调用方以此感知连接关闭。
