# CLAUDE.md — ztunnel 根导航（L0）

> 本项目文档体系采用**渐进式披露**：本文件只保留全局必需信息，细节按层级下沉到子目录的 CLAUDE.md，AI 按需加载对应层级。
>
> 层级规则：**L0 根导航（本文件）→ L1 模块文档（cmd/common/client/server/engine/testutil）→ L2 子系统文档（engine/net、engine/net/middleware）**。任何事实只在最深一级出现，上层只放一句话摘要 + 链接，避免重复。

## 项目定位

ztunnel 是一个 NAT 穿透（内网穿透）工具：TCP 协议、零第三方依赖、可选自定义加密。client-server 架构：公网服务器反向代理端口到内网服务。

- Go 1.24，`go.mod` 无任何 require（**零第三方依赖是硬性约束，测试代码同样不引入外部包**）
- 单一 module `ztunnel`，所有 import 以 `ztunnel/` 开头
- 用户文档：[Readme.md](Readme.md)（英）/ [ReadmeCh.md](ReadmeCh.md)（中）
- 测试：48 个测试函数（单元/handler/崩溃回归 + `-race` 全量可用），见 [testutil/CLAUDE.md](testutil/CLAUDE.md)

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
| `testutil/` | 测试辅助库（崩溃探针/链桩/假会话） | [testutil/CLAUDE.md](testutil/CLAUDE.md) |

## 隧道生命周期（细节见 [common/CLAUDE.md](common/CLAUDE.md)）

1. **建隧道**：outclient 连上 inserver 后发 `CreateTunnel(token, outPort)`；inserver 校验 token，同步绑定 outPort 并把 outserver 绑定到该会话。
2. **新连接**：终端用户连入 outserver → 控制通道发 `ConnectNew(connectId)` → outclient 建一个 inclient 连到真实服务，回 `ConnectNew(code, connectId)`。
3. **传数据**：双向数据均包装为 `ConnectData(connectId, data)` 在控制通道传输。
4. **断连**：任一侧断开发 `ConnectDelete(connectId)`；控制通道断开则 server 停掉对应 outserver 及其全部用户会话。

## 构建与运行

```sh
# 交叉编译 4 个二进制（linux/windows x64，产物位置见 cmd/CLAUDE.md）
cd cmd && ./build.bat

# 服务端（公网机器）
./ztunnel_server_linux_x64 -listen=:8888 -net_encrypt=true -token=mytoken

# 客户端（内网机器）
./ztunnel_client_windows_x64.exe -server=1.2.3.4:8888 -export_port=3307 \
    -forward=192.168.0.100:3306 -net_encrypt=true -token=mytoken
```

完整 flag 参数表见 [cmd/CLAUDE.md](cmd/CLAUDE.md)。

## 全局约定

- **零依赖**：任何代码（含测试）不得引入第三方包。
- **接口优先**：engine 全部能力以 interface 暴露（`NetClient`/`NetServer`/`Codec`/`Middleware`/`Conn`/`Queue`），实现私有，`NewXxx()` 工厂创建。
- **并发模型**：每连接固定 2 个 goroutine（收/发），发送走无界队列——细节见 [engine/net/CLAUDE.md](engine/net/CLAUDE.md)。
- **错误处理**：底层 error 由上层记日志；服务端 `OnMessage` 返回 error 会**关闭该连接**（工厂包装层行为，见 [common/CLAUDE.md](common/CLAUDE.md)）；连接收发循环有 recover 兜底，单连接 panic 不杀进程（见 [engine/net/CLAUDE.md](engine/net/CLAUDE.md)）。
- **字节序**：协议字段一律大端。
- **命名风格**：接收者名混用 `this`/`c`/`m`（历史遗留），新代码遵循就近文件已有风格。

## 已知问题（修改相关代码前必读，细节在对应模块文档）

| 问题 | 位置 |
|---|---|
| `build.bat` 产物落在 `cmd/` 而非仓库根目录 | [cmd/CLAUDE.md](cmd/CLAUDE.md) |
| **token 为空（默认）即无鉴权**，且无端口白名单/申请限速——持任意 token 的客户端可要求服务端监听任意端口 | [common/CLAUDE.md](common/CLAUDE.md) / [server/CLAUDE.md](server/CLAUDE.md) |
| 加密握手用 RSA-1024（强度弱，属设计取舍） | [engine/net/middleware/CLAUDE.md](engine/net/middleware/CLAUDE.md) |
| type1 自研加密为 8 字节掩码 XOR 且无服务端身份验证（公钥不固定），仅能对抗被动窃听，主动 MITM 可解密全部流量；建议迁移 `crypto/tls`（标准库，仍满足零依赖约束） | [engine/net/middleware/CLAUDE.md](engine/net/middleware/CLAUDE.md) |
| 健壮性缺口：无应用层心跳（NAT 半开连接无感知）、发送队列无界（慢消费者内存增长）、无连接/读写超时、未设 TCP_NODELAY | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
| ws 传输层整体被注释掉，仅 socket 可用 | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
