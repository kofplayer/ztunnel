# client/ — 客户端业务逻辑（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。两类组件：`outclient`（控制通道客户端）和 `inclient`（每个终端用户连接对应一个、连内网真实服务的转发客户端）。协议格式与消息时序见 [../common/CLAUDE.md](../common/CLAUDE.md)，engine 机制见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)。

## outclient/ — 主客户端

`outclient.NewClient(host, port, svrListenPort, forwardHost, forwardPort)` 按当前 `proto.NetEncrypt` 选择控制通道中间件栈（栈表见 [../common/CLAUDE.md](../common/CLAUDE.md)）。

**handler 行为**：

- `OnReady()`：连接就绪后发送 `CreateTunnel(token, outPort)`，并用 **`time.AfterFunc`（10s）** 挂超时回调——到期未收到响应则 `Stop()` 整个客户端（外层 [../cmd/](../cmd/CLAUDE.md) 重连循环接管）。不用常驻 goroutine 阻塞等 timer（历史实现每次建隧道泄漏一个 goroutine），timer 字段由 `timerMu` 保护（OnReady 与 OnMessage 运行在不同 goroutine）。
- `OnMessage()`：四个分支**解析前都校验长度**（不足返回 error）：
  - `CreateTunnel`：停定时器；`code≠0` → Stop。
  - `ConnectNew`：connectId → `inClientMgr.OpenClient()` 连真实服务 → 回 `ConnectNew(code, connectId)`。
  - `ConnectData`：按 connectId 找到 inclient，剥掉前 4 字节后转发。
  - `ConnectDelete`：关闭对应 inclient。
- `OnDisconnect()`：向 `c` channel 发 false 解除 `Start()` 阻塞。

**Start/Stop 语义**：`Start()` = `Connect()`（阻塞至就绪/失败，见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)）+ `<-c.c`；`Stop()` = 向 `c.c` 发信号 + 关闭全部 inclient。channel 容量 2，承载三路信号（对端断开 / 建隧道超时 / 失败响应），最坏时序下不阻塞重连循环。

## inclient/ — 转发客户端

- `inclient.NewClient(connectId, host, port, outcli, mgr)`：为单个 connectId 建 TCP 连到真实服务，栈为 `fullData + type0` + `codec_data`（纯透传，无分包无加密）。
- `handler.OnMessage()`：真实服务返回的数据包装成 `ConnectData(connectId, data)` 发回控制通道。
- `handler.OnDisconnect()`：真实服务断开 → 发 `ConnectDelete(connectId)` 通知服务端关闭对应用户会话，再调 `mgr.RemoveClient(connectId)` 清理 map（该回调运行在 inclient 自己的 receiver goroutine，跨 goroutine 写 map，mgr 必须持锁）。

## inclient/mgr.go — ClientMgr

`mu sync.Mutex` + `map[SessionID]NetClient` 管理 connectId → inclient 映射，全部方法持锁。方法：`OpenClient`（建+连）、`GetClient`、`CloseClient`（先删 map 再 Disconnect，避免与 `RemoveClient` 竞争）、`RemoveClient`（连接已断时仅删 map、不 Disconnect）、`CloseAllClient`。

⚠️ **已知问题**：
1. **OpenClient 窄窗口泄漏**：`cli.Connect()` 成功返回到写入 map 之间，若真实服务 accept 后立刻断开，`OnDisconnect` 先执行 `RemoveClient` 删空，随后死 client 仍被插入 map（窗口极小）。
2. `GetClient` 对不存在的 key 返回 nil 接口，调用方靠 nil 判断兜底。

> 历史问题「控制通道 SendMessage 并发竞态」已修复：type1 加密中间件的发送路径已串行化（`sendMu`），机制见 [../engine/net/middleware/CLAUDE.md](../engine/net/middleware/CLAUDE.md)。
