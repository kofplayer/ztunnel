# server/ — 服务端业务逻辑（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。`inserver`（监听控制端口、接受客户端建隧道）和 `outserver`（每条隧道一个、监听公网暴露端口、面向终端用户）。协议格式见 [../common/CLAUDE.md](../common/CLAUDE.md)。

## inserver/ — 控制服务器

`inserver.NewServer(host, port)` 按 `proto.NetEncrypt` 选栈（见 [../common/CLAUDE.md](../common/CLAUDE.md)），handler 处理控制通道消息：

- `CreateTunnel`（c→s）：
  1. 校验长度 `len(data) == TokenLen+2`、会话**未绑定过** bindObject、token 匹配，任一失败返回 error → **连接被工厂包装层关闭**（见 [../common/CLAUDE.md](../common/CLAUDE.md) error 语义）。
  2. 解析 outPort → `outserver.NewServer("", outPort, s)` 创建公网监听。
  3. `s.SetBindObject(svr)`：**把 outserver 绑定到该控制会话**（bindObject 是 NetSession 上的任意附加字段，机制见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)）。
  4. `go svr.Start()` 并回 `CreateTunnel(ErrorCodeNone)`。
- `ConnectNew`（c→s，带 code）：code≠0 → 从 bindObject(outserver) 的 SessionMgr 找到 connectId 对应会话并 Close（真实服务连不上，断开终端用户）。
- `ConnectDelete`：同上，关闭对应用户会话。
- `ConnectData`：剥 connectId → 找 outserver 会话 → 剥头后原样 `SendMessage(0,0,...)` 转发给终端用户。
- `OnDisconnect`：客户端断开 → 取 bindObject(outserver) → `Stop()` 停止公网监听。

⚠️ **已知问题**：各分支虽有长度校验（`data error1~5` 日志），但 `MsgIdConnectNew`（code≠0 分支）与 `MsgIdConnectDelete` 中 `s.GetBindObject().(netServer.NetServer)` **未判 nil**——建隧道之前收到这两类消息会类型断言 panic；只有 `MsgIdConnectData` 判了 nil（返回 `status error2`）。其余畸形包返回 error 后连接被关闭，符合预期。

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
