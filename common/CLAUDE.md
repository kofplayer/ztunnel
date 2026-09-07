# common/ — 协议与装配层（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。定义隧道线上协议（proto）、隧道消息时序、把 engine 组件装配成可用 client/server 的工厂、地址工具。被 [../client/](../client/CLAUDE.md) 和 [../server/](../server/CLAUDE.md) 共同依赖。

## 文件

| 文件 | 职责 |
|---|---|
| `proto/proto.go` | 隧道消息 ID、消息体格式、全局 Token/NetEncrypt、SessionId 读写 |
| `client/client.go` | 客户端装配工厂 + `IClientHandler` 接口 |
| `server/server.go` | 服务端装配工厂 + `IServerHandler` 接口 |
| `util/util.go` | `GetHostAndPort` 拆分地址（port 1~65535；取**最后一个** `:` 分隔，兼容 IPv6 字面量，host 保留方括号返回以便直接拼接拨号） |

## 线上协议（proto.go）

应用层消息格式：`msgId` 由 codec 的 type 字节承载（控制通道用 `codec_type8_data`），data 部分按下表（全部大端）：

| msgId | 常量 | 方向 | data 格式 |
|---|---|---|---|
| 1 | `MsgIdCreateTunnel` | c→s | `token(TokenLen字节) \| outPort(2B)` |
| | | s→c | `code(1B)`，0=成功 |
| 2 | `MsgIdConnectNew` | s→c | `connectId(4B)` |
| | | c→s | `code(1B) \| connectId(4B)`，0=成功 |
| 3 | `MsgIdConnectData` | 双向 | `connectId(4B) \| 任意数据` |
| 4 | `MsgIdConnectDelete` | 双向 | `connectId(4B)` |

- `connectId` 即 `netSession.SessionID`（uint32），`ReadSessionId`/`WriteSessionId` 做大端转换。
- 错误码：`ErrorCodeNone=0`、`ErrorCodeNormal=1`。
- **全局变量** `proto.Token`/`TokenLen`/`NetEncrypt`：由 [../cmd/](../cmd/CLAUDE.md) 入口在启动时设置，决定鉴权与中间件栈选择——**必须在装配 client/server 之前设置**。
- ⚠️ **token 为空（默认）即无鉴权**：任何能连上控制端口的客户端都能要求监听任意端口；且无端口白名单与申请限速。公网部署必须设置强 token。
- ⚠️ token 比较用 `==`（非常量时间比较），安全敏感场景可改为 `subtle.ConstantTimeCompare`。

## 隧道消息流（时序，与两侧 handler 的对应关系见 client/server 文档）

1. **建隧道**：outclient 连上 inserver → 发 `CreateTunnel(token, outPort)` → inserver 校验长度/未绑定/token（任一失败返回 error → 连接被关闭），同步绑定 outPort（被占或 0 回错误码），成功后把 outserver 绑定到控制会话并回 `CreateTunnel(ErrorCodeNone)`。
2. **新连接**：终端用户接入 outserver → 控制通道发 `ConnectNew(connectId)` → outclient 为该 connectId 建 inclient 连真实服务 → 回 `ConnectNew(code, connectId)`；`code≠0` 表示真实服务连不上，server 关闭该用户会话。
3. **传数据**：双向数据均包装为 `ConnectData(connectId, data)`。
4. **断连**：任一侧断开发 `ConnectDelete(connectId)`，对端关闭对应会话/inclient；**控制通道断开**则 server 停掉 outserver（`Stop` 同时回收其全部用户会话）。

## 装配工厂

两个工厂把「socket 连接器 + codec + 中间件列表 + handler 回调」组装成 engine 的 `NetClient`/`NetServer`：

- `client.NewClient(host, port, handler, codec, middlewares)` → 内部创建 `socketNetConnect.NewConnector()`
- `server.NewServer(host, port, handler, codec, middlewares)` → 内部创建 `socketNetConnect.NewAcceptor()`
- `server.StartServer(svr)`：`go svr.Start()`，失败直接 `panic`（⚠️ 当前无调用方，属死代码）

**Handler 接口**（业务侧实现，见 client/server 模块文档）：

```go
IClientHandler: OnConnect() / OnReady() / OnDisconnect() / OnMessage(cb, msgID, data) error
IServerHandler: 同上但每个回调带 netSession.NetSession 参数
```

⚠️ **服务端 OnMessage 的 error 语义**：工厂包装了回调，`OnMessage` 返回 error 会**直接关闭该连接**（`s.Close()`）——业务 handler 返回 error 前要意识到这一点。

## 中间件栈配置（谁配什么栈）

栈的选择逻辑分散在业务侧（`outclient.NewClient` / `inserver.NewServer` / `inclient.NewClient` / `outserver.NewServer`），统一规则：

| 通道 | codec | 中间件（顺序即链序） |
|---|---|---|
| 控制通道（outclient↔inserver），明文 | `type8_data` | `len4Data` → `type0Encrypt` |
| 控制通道，加密（`NetEncrypt=true`） | `type8_data` | `len4Data` → `type1Encrypt` → `verifier` |
| 数据通道（outserver↔终端用户、inclient↔真实服务） | `data`（透传） | `fullData` → `type0Encrypt` |

中间件顺序**不可调换**：分包必须在最外（靠网线侧），校验必须在加密之内（靠应用侧）。原因与各组件细节见 [../engine/net/middleware/CLAUDE.md](../engine/net/middleware/CLAUDE.md)；codec 说明见 [../engine/net/CLAUDE.md](../engine/net/CLAUDE.md)。
