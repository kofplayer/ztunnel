# client/ — 客户端业务逻辑（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。两类组件：`outclient`（连公网 server 的主通道客户端）和 `inclient`（每个终端用户连接对应一个、连内网真实服务的转发客户端）。协议格式见 [../common/CLAUDE.md](../common/CLAUDE.md)，engine 机制见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)。

## outclient/ — 主客户端

`outclient.NewClient(host, port, svrListenPort, forwardHost, forwardPort)` 按当前 `proto.NetEncrypt` 选择控制通道中间件栈（见 [../common/CLAUDE.md](../common/CLAUDE.md) 栈配置表）。

**handler 行为**：

- `OnReady()`：连接建立且（若加密）握手完成后触发 → 发送 `CreateTunnel(token, outPort)`，同时起 **10s 超时定时器**，超时未收到响应则 `Stop()` 整个客户端（外层 [../cmd/](../cmd/CLAUDE.md) 重连循环会接管）。
- `OnMessage()` 按 msgID 分发：
  - `CreateTunnel`：停定时器；`data[0]!=0` 则 Stop。⚠️ **未校验 data 长度**，空包会越界 panic。
  - `ConnectNew`：取 connectId → `inClientMgr.OpenClient()` 连真实服务 → 回 `ConnectNew(code, connectId)`。⚠️ 未校验长度。
  - `ConnectData`：取 connectId → 找到对应 inclient → 剥掉前 4 字节 connectId 后原样 `SendMessage(0,0,data)` 转发。
  - `ConnectDelete`：关闭对应 inclient。
- `OnDisconnect()`：向 `c` channel 发 false 解除 `Start()` 阻塞。

**Start/Stop 语义**：`Start()` = `Connect()` + `<-c.c`（阻塞直到断开/失败）；`Stop()` = 往 `c.c` 发信号 + 关闭全部 inclient。channel 容量 2。

## inclient/ — 转发客户端

- `inclient.NewClient(mgr, connectId, host, port, outcli)`：为单个 connectId 建 TCP 连到真实服务，栈为 `fullData + type0` + `codec_data`（纯透传，无分包无加密）。
- `handler.OnMessage()`：把真实服务返回的数据包装成 `ConnectData(connectId, data)` 发回 outclient → 控制通道。
- `handler.OnDisconnect()`：真实服务断开 → 发 `ConnectDelete(connectId)` 通知服务端关闭对应用户会话，再调 `mgr.RemoveClient(connectId)` 清理本地 map 条目（该回调运行在 inclient 自己的 receiver goroutine 上，跨 goroutine 写 map，因此 mgr 必须持锁）。

## inclient/mgr.go — ClientMgr

`mu sync.Mutex` + `map[SessionID]NetClient` 管理 connectId → inclient 映射，全部方法持锁。方法：`OpenClient`（建+连）、`GetClient`、`CloseClient`（先删 map 再 Disconnect，避免与 `RemoveClient` 竞争）、`RemoveClient`（连接已断时仅删 map、不 Disconnect）、`CloseAllClient`。

⚠️ **已知问题：**
1. **OpenClient 存在窄窗口泄漏**：`cli.Connect()` 成功返回到写入 map 之间，若真实服务 accept 后立刻断开，`OnDisconnect` 先于插入执行 `RemoveClient` 会删空，随后死 client 仍被插入 map（窗口极小）。历史上的 mgr map 无锁并发问题（含 `CloseAllClient` 经 `Stop()` 与控制通道迟到 `OnMessage` 并发 `clear(map)` 的 data race）已通过给 mgr 加 `sync.Mutex` 修复。
2. **控制通道 `SendMessage` 并发竞态（更重要）**：`outclient.OnMessage`（控制通道 receiver goroutine）与每个 inclient 的 `OnMessage/OnDisconnect`（各自的 receiver goroutine）都会调 `c.cli.SendMessage`；`net_encrypt=true` 时 type1 的 `SendData` 无锁演化 `CsNo`（见 [../engine/net/middleware/CLAUDE.md](../engine/net/middleware/CLAUDE.md)），并发下既 data race 又会序号失步 → 对端解密失败断连。type0 栈中间件无状态+队列带锁，恰好安全。服务端对称存在（inserver 会话被多个 outserver handler 并发 SendMessage）。
3. `GetClient` 对不存在的 key 返回 nil 接口（Go map 语义），调用方靠 nil 判断兜底。

## 数据流总览

```
内网真实服务 ◄─TCP─ inclient(N个) ◄─ outclient ◄═══控制通道═══► inserver(公网)
     ▲                │  ConnectData(cid,data)         │
     └── 转发剥掉cid ──┘                                ▼
                                              outserver(公网监听) ◄─ 终端用户
```

修改建议落点：给**控制通道发送路径**加一把 mutex（包在 `SendMessage` 外层）可消除问题 2 的全部风险；再让 `Stop()` 补一句 `cli.Disconnect()` 真正停掉 receiver goroutine。mgr 的 `sync.Mutex` 已加上（本页历史问题 1 由此修复），但它只覆盖 map 并发，不解决发送路径竞态。
