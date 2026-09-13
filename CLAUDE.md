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

## 已知问题与硬约束（修改相关代码前必读，细节在对应模块文档）

### 改代码前必须遵守的不变式

| 约束 | 原因 |
|---|---|
| **持锁路径绝不调用 `Disconnect()`/`Close()`** | 断开通知现在会在调用 goroutine 内**同步**派发整条 OnDisconnect 链，重入业务自己的锁即自死锁。既有两处已修（`netServer` 四回调判空、`ClientMgr.CloseAllClient` 锁内快照锁外断连）；新代码同遵 |
| **`FireEvent` 只能在接完整的链上调用** | 链头若不是 `NetMiddlewareFirst`，`First()` 会返回自身导致 `FireEvent` **无限自我递归 → 栈溢出**，而 Go 的栈溢出是 fatal error，`recover` 拦不住（CRASH-04，已加守卫 + 崩溃探针用例） |
| **type1 锁序固定为 `sendMu → mu`** | 持 `mu` 再取 `sendMu` 会与握手路径形成 AB-BA 死锁；握手末帧必须在该串行域内交付 |
| **`-net_encrypt=true` 两端必须同版本** | 2026-09-13 的掩码修正（CRYPT-01）与 2048 公钥策略下限都是**线上断代**改动，新旧混跑握手必然失败。协议至今无版本字段 |

### 仍然存在的问题

| 问题 | 位置 |
|---|---|
| **token 为空（默认）即无鉴权**，且无端口白名单/申请限速——持任意 token 的客户端可要求服务端监听任意端口。启动时会打 `SECURITY:` 告警，但仍靠运维自觉 | [common/CLAUDE.md](common/CLAUDE.md) / [server/CLAUDE.md](server/CLAUDE.md) |
| **`-net_encrypt` 是混淆不是加密**：无服务端身份验证（主动 MITM 可拿下 token 并解全部流量）、无 MAC（verifier 的 XOR 和对字节置换不变）、帧长在最外层明文、服务端 RSA 私钥全连接共用 → 无前向保密。根治 = 迁移 `crypto/tls` + 公钥 pinning | [engine/net/middleware/CLAUDE.md](engine/net/middleware/CLAUDE.md) |
| 健壮性缺口：无应用层心跳（NAT 半开连接无感知）、发送队列无界（慢消费者内存增长）、**握手阶段无超时且无连接数上限**（预认证资源耗尽）、未设 TCP_NODELAY | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
| 无半关闭语义：任一方向 FIN 即双向拆除，`Connection: close` / `nc` / SMTP-FTP `QUIT` 类协议会看到响应截断 | [server/CLAUDE.md](server/CLAUDE.md) |
| `log` 包轮转无同步（fd 泄漏/日志丢失/竞态），`Fatal` 级别既不 exit 也不区别对待；日志落相对路径 `./log`，**无大小上限、无清理** | [engine/CLAUDE.md](engine/CLAUDE.md) |
| `OpenClient` 在控制通道唯一 receiver goroutine 上同步拨号 → 单条用户连接可头阻塞整条隧道；用户一连上即拨内网服务且无每隧道上限 | [client/CLAUDE.md](client/CLAUDE.md) |
| 会话 ID 为 uint32 自增且**绝不复用**（无串流风险），但回绕后 `sessions[id]` 会被静默覆盖（`OpenClient` 已加身份核对，`NewSession` 尚未） | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
| ws 传输层整体被注释掉，仅 socket 可用 | [engine/net/CLAUDE.md](engine/net/CLAUDE.md) |
| `engine/net/connect/ws/` 189 行注释代码常驻树中，建议移入分支或删除 | — |
