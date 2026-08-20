# CLAUDE.md — ztunnel 根导航（L0）

> 本项目文档体系采用**渐进式披露**：本文件只保留全局必需信息，细节按层级下沉到子目录的 CLAUDE.md，AI 按需加载对应层级。
>
> 层级规则：**L0 根导航（本文件）→ L1 模块文档（cmd/common/client/server/engine）→ L2 子系统文档（engine/net、engine/net/middleware）**。任何事实只在最深一级出现，上层只放一句话摘要 + 链接，避免重复。

## 项目定位

ztunnel 是一个 NAT 穿透（内网穿透）工具：TCP 协议、零第三方依赖、可选自定义加密。client-server 架构：公网服务器反向代理端口到内网服务。

- Go 1.24，`go.mod` 无任何 require（**零第三方依赖是硬性约束，新增代码不得引入外部包**）
- 单一 module `ztunnel`，所有 import 以 `ztunnel/` 开头
- 用户文档：[Readme.md](Readme.md)（英）/ [ReadmeCh.md](ReadmeCh.md)（中）
- 项目无任何测试文件

## 架构总览（一句话版）

内网 client 连接公网 server 建立控制通道，server 在公网开监听端口；终端用户流量经 server 转发到 client，client 再转发到内网真实服务。所有连接复用 engine 的「中间件链 + 编解码器」网络框架。

```
终端用户 ──TCP──> server:export_port (outserver·每隧道一个)
                     │  ConnectData/Delte(connectId+data)
                     ▼
公网 server:listen (inserver) ◄═══ 控制通道(可加密) ═══► 内网 client (outclient)
                                                            │
                                                    inclient(每用户连接一个)
                                                            ▼
                                                    内网真实服务 forward_host:port
```

## 目录导航（L1 文档索引）

| 目录 | 职责 | 详细文档 |
|---|---|---|
| `cmd/` | 两个 main 入口 + 交叉编译脚本 | [cmd/CLAUDE.md](cmd/CLAUDE.md) |
| `common/` | 隧道消息协议 proto + 客户端/服务器装配工厂 | [common/CLAUDE.md](common/CLAUDE.md) |
| `client/` | 客户端业务逻辑（outclient 主通道 + inclient 转发） | [client/CLAUDE.md](client/CLAUDE.md) |
| `server/` | 服务端业务逻辑（inserver 控制 + outserver 公网监听） | [server/CLAUDE.md](server/CLAUDE.md) |
| `engine/` | 可复用引擎：net 网络框架 / log 日志 / queue 队列 | [engine/CLAUDE.md](engine/CLAUDE.md) |
| `engine/net/` | 网络框架核心抽象（L2） | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
| `engine/net/middleware/` | 中间件实现：分包/加密/校验（L2） | [engine/net/middleware/CLAUDE.md](engine/net/middleware/CLAUDE.md) |

## 隧道生命周期（细节见 [common/CLAUDE.md](common/CLAUDE.md)）

1. **建隧道**：outclient 连上 inserver 后发 `CreateTunnel(token, outPort)`；inserver 校验 token，在 outPort 起一个 outserver 监听并绑定到该会话。
2. **新连接**：终端用户连入 outserver → 通过控制通道发 `ConnectNew(connectId)` → outclient 为该 connectId 建一个 inclient 连到真实服务，回 `ConnectNew(code, connectId)`。
3. **传数据**：双向数据均包装为 `ConnectData(connectId, data)` 在控制通道传输。
4. **断连**：任一侧断开发 `ConnectDelete(connectId)`；控制通道断开则 server 停掉对应 outserver 监听。

## 构建与运行

```sh
# 交叉编译 4 个二进制（linux/windows x64，产物位置见下表）
cd cmd && ./build.bat

# 服务端（公网机器）
./ztunnel_server_linux_x64 -listen=:8888 -net_encrypt=true -token=mytoken

# 客户端（内网机器）
./ztunnel_client_windows_x64.exe -server=1.2.3.4:8888 -export_port=3307 \
    -forward=192.168.0.100:3306 -net_encrypt=true -token=mytoken
```

完整 flag 参数表见 [cmd/CLAUDE.md](cmd/CLAUDE.md)。

## 全局约定

- **日志**：统一用 `log.Main().Info/Warn/Error(fmt, args...)`（printf 风格），日志写入 `./log/` 目录按小时轮转；日志级别常量 `log.DEBUG~NONE`（0~5）。
- **接口优先**：engine 全部能力以 interface 暴露（`NetClient`/`NetServer`/`Codec`/`Middleware`/`Conn`/`Queue`），实现为私有结构体，通过 `NewXxx()` 工厂函数创建。
- **错误处理**：底层返回 error 由上层记录日志；服务端 `OnMessage` 返回 error 会导致连接被关闭（见 [common/CLAUDE.md](common/CLAUDE.md)）。
- **字节序**：所有协议字段一律大端（`binary.BigEndian`）。
- **命名风格**：接收者名混用 `this`/`c`/`m`（历史遗留），新代码遵循就近文件已有风格即可。
- **goroutine 模型**：每连接固定 2 个 goroutine（`receiverRun`/`senderRun`）+ 发送走无界队列（见 [engine/CLAUDE.md](engine/CLAUDE.md)）。

## 已知问题索引（修改相关代码前必读，细节在各模块文档）

| 问题 | 位置 |
|---|---|
| `log.Warn` 级别判断用错常量，log_level≥2 时 WARN 被吞 | [engine/CLAUDE.md](engine/CLAUDE.md) |
| `outclient`/`inserver` 的 `OnMessage` 解析前未校验长度，畸形包可 panic | [client/CLAUDE.md](client/CLAUDE.md) / [server/CLAUDE.md](server/CLAUDE.md) |
| `build.bat` 产物落在 `cmd/` 而非仓库根目录 | [cmd/CLAUDE.md](cmd/CLAUDE.md) |
| 加密握手用 RSA-1024（强度弱，属设计取舍） | [engine/net/middleware/CLAUDE.md](engine/net/middleware/CLAUDE.md) |
| ws 传输层整体被注释掉，仅 socket 可用 | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
