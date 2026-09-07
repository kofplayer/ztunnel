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
- `SessionMgr`：自增 ID 分配（**锁内自增**）+ map 管理，`NewSession`/`RemoveSession`/`GetSession`/`TravelSession`（读写锁保护）。
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

- `ConnSocket`：**每连接固定 2 个 goroutine**（`receiverRun`/`senderRun`）；发送走无界队列（见 [../CLAUDE.md](../CLAUDE.md) queue 节，初始容量 32），读缓冲 4096B。
- **recover 兜底**：`receiverRun`/`senderRun` 入口与 accept 回调（`safeAccept`）均 recover——链上/业务 handler 的任何 panic 按「关闭该连接」处理（`Abort()`：关发送队列 + 立即关 TCP），单连接异常不杀进程。
- 断开语义 = 关队列（`senderRun` 排空退出并关 TCP）+ 触发 `onDisconnectFunc`（单次；q 已关时跳过，防重复触发）。
- `ConnectorSocket`：内嵌 ConnSocket，`Connect()` 拨号 + keepalive(30s) + 起收发 goroutine。
- `AcceptorSocket`：`Listen()` 预绑定端口；`Start()` 复用已绑定 listener（否则自行绑定）后阻塞 accept 循环（返回即 listener 出错）。

⚠️ **健壮性缺口（已知，未修）**：
- 发送队列**无界**（满了翻倍扩容）：慢消费者 + 快生产者会持续吃内存，无背压。
- **无应用层心跳**：仅 TCP keepalive，NAT 映射失效/半开连接双方长期无感知。
- **无超时**：拨号无 deadline（`net.Dial` 裸调），收发循环无读写 deadline；未设 `TCP_NODELAY`（交互式协议延迟受损）。
