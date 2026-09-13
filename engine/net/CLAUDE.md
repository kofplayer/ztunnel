# engine/net/ — 网络框架核心（L2）

> 上级：[../CLAUDE.md](../CLAUDE.md)。分层事件驱动网络框架，业务侧（[../../common/](../../common/CLAUDE.md)）只接触工厂与 handler 回调。中间件各实现的细节在 [middleware/CLAUDE.md](middleware/CLAUDE.md)。

## 目录与核心抽象

| 子目录 | 接口 | 说明 |
|---|---|---|
| `connect/` | `Conn` / `Connector` / `Acceptor` | 传输层抽象（Connector 内嵌 Conn；**Acceptor 含 `Listen()` 预绑定**） |
| `connect/socket/` | 上述接口的 TCP 实现 | **当前唯一可用实现** |
| `connect/ws/` | — | WebSocket 实现，**整体被注释**（依赖 gorilla/websocket，与零依赖原则冲突而搁置） |
| `session/` | `NetSession` / `SessionMgr` | 服务端连接会话（ID 分配、bindObject 附加数据） |
| `client/` | `NetClient` | 客户端：组装中间件链，`Connect()` 阻塞至握手完成 |
| `server/` | `NetServer` | 服务端：每 accept 一条连接组装一条独立中间件链 |
| `codec/` | `Codec` | 应用层消息编解码（msgID 头格式） |
| `middleware/` | `Middleware` | 处理管线（分包/加密/校验），见 [middleware/CLAUDE.md](middleware/CLAUDE.md) |

## 中间件链机制（本框架核心）

每条连接（client 一条 / server 每 accept 一条）构建一条**双向**链：

```
First ──> mw1 ──> mw2 ──> ... ──> Last
(socket侧)                              (应用侧)
```

- **接收方向** `ReceiveData`：socket 数据 → `First.ReceiveData` → 各 mw 默认实现调 `Next()` → `Last.ReceiveData`（内做 `codec.Decode` + 业务 `onMessage`）。
- **发送方向** `SendData`：业务 `SendMessage` → `codec.Encode` → `Last.SendData` → 各 mw 默认实现调 `Pre()` → `First.SendData`（写 socket 发送队列）。
- **事件传播** `FireEvent`：从 First 沿 Next 链传播 `OnConnect`/`OnDisconnect`/`OnReady`；`NetMiddlewareFirst` 的重入队列**线程安全**（事件可来自拨号与接收两个 goroutine，firing 期间新事件排队、锁外执行回调）。加密中间件在握手完成后才 `FireEvent(OnReady)`。
- ⚠️ 中间件**每连接一个实例**（server 侧由 `CreateMiddlewareFunc` 工厂按连接创建），实例内可安全持有会话状态（如握手状态机、分包残包缓冲）——但跨 goroutine 的读写仍需自行加锁（参考 type1 的 `mu`/`sendMu` 分工）。

## client/ — NetClient 行为

`Connect()` 关键点：构建链 → `go connector.Connect()` → **阻塞于 cap 1 的 result channel，三者先到先得**：拨号失败 / 握手完成（OnReady）/ 握手完成前断开（原子标记 `handshakeComplete` 判定）。即 type0 栈在 OnConnect 后立即 ready；type1 栈要等加密握手完成（见 [middleware/CLAUDE.md](middleware/CLAUDE.md)）。用 channel + 原子标记而非共享 err 变量（历史上 err 跨 goroutine 读写是 data race）。

注意：拨号成功但握手无响应且连接不断时 `Connect()` 不返回（无应用层超时——调用方 outclient 的 10s 定时器只覆盖「已就绪后等 CreateTunnel 应答」阶段）。

`SendMessage(cb, msgID, data)`：`codec.Encode` 后从 **Last** 进入链。`Disconnect()` 委托 connector。

## server/ — NetServer 行为

- `Start()`：acceptor 每 accept 一条连接 → 独立建链 → `sessionMgr.NewSession()` → session 绑定 conn 与 `SendMessageFunc` → 触发 `OnConnect` 事件 + 业务 `onAccept`。断开时触发 `OnDisconnect` 事件 + 业务回调 + `RemoveSession`。
- `Listen()`：**预绑定端口但不进 accept 循环**——供调用方在 Start 前确认端口可用（inserver 建隧道的同步应答依赖它）。
- `Stop()`：**先经 `TravelSession` 关闭全部存量会话**（否则已建立的连接悬挂泄漏），再停 listener。
- `GetSessionMgr()`：业务靠它按 connectId 找会话（[../../server/](../../server/CLAUDE.md) 的转发就靠这个）。

## session/ — 会话

- `SessionID = uint32`，`SessionIDSize = 4`（协议中 connectId 的字节数）。
- `SessionMgr`：自增 ID 分配（**锁内自增**）+ map 管理，`NewSession`/`RemoveSession`/`GetSession`/`TravelSession`/`Len`（读写锁保护）。
  - `TravelSession` **锁内只做快照、回调一律在锁外**：回调链可能反过来取写锁（`Close` → 断开通知 → `RemoveSession`），而 `RWMutex` 不记持有者、writer 排队时又会阻塞后续 `RLock` → 同 goroutine 内即自死锁。回调必须对已被并发改动的会话幂等。
  - `Len()` 供运行时监控与测试断言"会话已被回收"——没有它，map 泄漏几乎无法断言。
- `netSession` 的 `conn`/`bindObject`/`sendMessageFunc` 由内部 `RWMutex` 保护（它们会被**其它连接的** goroutine 读写，例如 inserver 在控制通道 receiver 上对用户会话调 `Close()`）。持锁纪律：`Close()` 只取引用即解锁，**不得**持锁跨过 `Disconnect()`。
- `Close()` **不再**把 `conn` 置 nil——OnDisconnect 回调仍需用它取 `RemoteAddr()` 记日志；`net.Conn` 关闭后调 `RemoteAddr()` 是安全的，幂等性由 `ConnSocket.disconnectOnce` 负责。
- `SendMessageFunc` 未设置时 `SendMessage` 返回 error（会话注册进 map 与回调绑定之间存在窗口，不可 nil 调用）。
- `bindObject`：`SetBindObject/GetBindObject`（`interface{}` 附加槽），inserver 用它把 outserver 绑到控制会话（见 [../../server/CLAUDE.md](../../server/CLAUDE.md)）。

## codec/ — 编解码器（4 种实现）

`Encode(cb, type, data) / Decode(pkg)`，决定 msgID 在字节流里的头部格式：

| 工厂 | 头部 | 用途 |
|---|---|---|
| `NewCodec_data()` | 无头，透传 | 数据通道（纯字节流转发） |
| `NewCodec_type8_data()` | `type(1B)` | **控制通道实际使用**（msgId 占 1 字节；⚠️ uint8 承载，>255 静默截断，扩展协议前需处理） |
| `NewCodec_type16_data()` | `type(2B BE)` | 未使用（备用） |
| `NewCodec_cb8_type16_data()` | `cb(1B)+type(2B BE)` | 未使用（备用） |

`cb` 参数全链路传 0，属预留。

## connect/socket/ — TCP 实现

- `ConnSocket`：**每连接固定 2 个 goroutine**（`receiverRun`/`senderRun`）；发送走无界队列（见 [../CLAUDE.md](../CLAUDE.md) queue 节，初始容量 32），读缓冲 4096B 直读 `conn`（原先另有一层 `bufio.Reader`，因只调 `Read` 且缓冲等长而完全不生效，已删除）。
- **断开语义 = `disconnectOnce` 保证「所有关闭路径恰好派发一次 OnDisconnect」**：`Disconnect()`（关队列 + 通知）、`Abort()`（关队列 + 立即关 TCP + 通知）、`receiverRun` 的读/处理错误、`senderRun` 的写错误、`recoverPanic` 五条路径统一走 `fireDisconnect()`。
  - ⚠️ **绝不能**改用 `q.IsClose()` 做门：该谓词描述队列状态、与「业务是否已通知」无关，而主动 Close 会先关队列——历史上正是它让 `RemoveSession`（全仓唯一调用点在 netServer 的断开闭包里）在主动关闭路径上整体不执行（报告 SEC-01）。
  - ⚠️ 断开通知在**调用 goroutine 内同步**跑完整条链，因此持锁路径不得调 `Disconnect()`/`Close()`，否则重入业务自己的锁即自死锁。
- `senderRun` 写失败必须 `Abort()`：此前是裸 `return`，既不关 fd（全仓再无别处关它）也不关队列，导致 fd 永久泄漏 + `SendData` 恒"成功"地把数据堆进无人消费的队列（报告 LEAK-02）。
- `ConnectorSocket`：内嵌 ConnSocket，`Connect()` 拨号 + keepalive(30s) + 起收发 goroutine；`onConnectFunc` 判空后调用。
- `AcceptorSocket`：`Listen()` 预绑定端口（判"已绑定且未关闭"，故 `Stop()` 后能真正重绑并如实报错）；`Start()` 复用已绑定 listener，**入口绑定一次、循环内不再重绑**（否则会把被 Stop 的端口重新占上）；`Stop()` 复位 `listener` 字段并回传关闭错误；`Accept` 的临时错误指数退避重试，仅 `net.ErrClosed`/已 Stop 才终止。
- **recover 兜底**：`receiverRun`/`senderRun` 入口、accept 回调（`safeAccept`）、以及 **client 侧拨号 goroutine**（2026-09-13 补，原先缺失）均 recover——链上/业务 handler 的任何 panic 按「关闭该连接」处理，单连接异常不杀进程。注意 `netSession.Close()` 不再把 `conn` 置 nil，断开回调里仍可取 `RemoteAddr()` 记日志。

⚠️ **健壮性缺口（已知，未修）**：
- 发送队列**无界**（满则翻倍）：慢消费者 + 快生产者持续吃内存，无背压；`Push` 用 `make` 扩容，溢出时会在**调用方 goroutine**里 panic。
- **无应用层心跳**：仅 TCP keepalive，NAT 映射失效/半开连接双方长期无感知。
- **握手阶段无任何超时、无连接数上限**：对端完成三次握手后静默即可让服务端的会话 + 2 个 goroutine + 整条中间件实例永久驻留（**预认证**资源耗尽）；客户端 `Connect()` 可无限阻塞。TCP keepalive 杀得死死对端，杀不死"活着但不握手"。
- 稳态连接也**无读写 deadline**；未设 `TCP_NODELAY`（交互式协议延迟受损）。
- 无半关闭语义：任一方向 FIN 即双向拆除。
