# server/ — 服务端业务逻辑（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。`inserver`（监听控制端口、接受客户端建隧道）和 `outserver`（每条隧道一个、监听公网暴露端口、面向终端用户）。协议格式与消息时序见 [../common/CLAUDE.md](../common/CLAUDE.md)。

## inserver/ — 控制服务器

`inserver.NewServer(host, port)` 按 `proto.NetEncrypt` 选栈（栈表见 [../common/CLAUDE.md](../common/CLAUDE.md)），handler 处理控制通道消息。**bindObject 访问统一走 `bindOutServer()`**：未建隧道（bindObject 为 nil）时返回 error 拒绝消息，而非对 nil 接口断言（历史上未认证连接发一条 `ConnectDelete` 即可 panic 崩溃进程）。

- `CreateTunnel`（c→s）：
  1. 校验长度 `len(data) == TokenLen+2`、会话**未绑定过** bindObject、token 匹配，任一失败返回 error → **连接被工厂包装层关闭**（见 [../common/CLAUDE.md](../common/CLAUDE.md) error 语义）。
  2. `outPort == 0` → 回错误码（不允许）。
  3. `outserver.NewServer("", outPort, s)` 后**同步 `svr.Listen()` 绑定端口**：被占用等失败立即回 `ErrorCodeNormal`（避免异步 Start 失败被静默吞掉后客户端收到"假成功"）；成功才 `s.SetBindObject(svr)`（bindObject 机制见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)）。
  4. `go svr.Start()` 并回 `CreateTunnel(ErrorCodeNone)`。
- `ConnectNew`（c→s，code≠0）：`bindOutServer` → 按 connectId 关闭对应用户会话（真实服务连不上，断开终端用户）。
- `ConnectDelete`：同上，关闭对应用户会话。
- `ConnectData`：剥 connectId → 找 outserver 会话 → 剥头后原样转发给终端用户。
- `OnDisconnect`：客户端断开 → bindObject 非空则 `outServer.Stop()`——**Stop 会关闭该 outserver 的全部存量用户会话**（见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)），不再有悬挂连接。

各分支解析前均校验长度（`data error1~5` 日志）。

⚠️ **无端口白名单**：任意持有效 token 的客户端可申请监听服务端任意端口（包括 22/80 等敏感端口），服务端侧不做限制。

## outserver/ — 公网暴露监听（每隧道一个）

`outserver.NewServer(host, port, inServerSession)`：栈为 `fullData + type0` + `codec_data`（透传），持有对控制会话的引用。

handler 行为（会话 = 一个终端用户连接）：

- `OnReady(s)`：终端用户接入完成 → 向控制通道发 `ConnectNew(s.GetID())`（connectId 即 outserver 本地 SessionMgr 分配的会话 ID）。
- `OnMessage(s)`：用户数据 → 包装 `ConnectData(s.GetID(), data)` → 控制通道。
- `OnDisconnect(s)`：用户断开 → 发 `ConnectDelete(s.GetID())`。

## connectId 的双映射关系

```
终端用户 ←→ outserver 会话 ID (=connectId, outserver.SessionMgr 分配)
                ↕ ConnectData/New/Delete 消息
         outclient 收到 connectId ←→ inclient map 键
```

同一 connectId 在 server 侧是 outserver 的会话 ID，在 client 侧是 inclient 的 map 键——**两端各自维护、仅通过消息同步**，删除时机也各自处理（收到对方的 Delete 消息或本地断开事件）。
