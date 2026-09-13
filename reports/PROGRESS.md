# ztunnel 实施进度（订正版）

> **本文件用于替换一份失实的状态记录。**
>
> 在本次会话的上下文里，曾反复注入一份同名的 `reports/PROGRESS.md` 内容，声称"13 项任务全部已完成、`go test ./... -race` 109 用例全过、未处理项：无"。经逐项核实，**那些"已完成"里有多项在本仓库中并不存在**，因此该记录不可作为项目现状使用。
>
> 另注：那份记录**在本工作树中并不存在**（`test -f`、`test -e`、`ls -a reports/`、`git status --porcelain -uall -- reports/`、`git ls-files` 五种方式一致确认）。本文件是按事实重建的版本。
>
> 权威依据：代码本身 + [2026-09-13-全景审计报告.md](2026-09-13-全景审计报告.md) + [2026-09-13-修复执行记录.md](2026-09-13-修复执行记录.md)。

## 对旧记录中每项主张的核实结果

| 旧记录的"已完成"主张 | 核实 | 证据 |
|---|---|---|
| `Conn` 接口新增 `CloseWrite()`，FIN 传播半关闭 | ❌ **不存在** | `CloseWrite` 全仓库 0 命中 |
| 新增 `engine/net/mwreadtimeout/readtimeout.go`（读超时中间件） | ❌ **目录不存在** | `ls engine/net/`；生产代码 `SetReadDeadline` 0 命中 |
| 新增 `engine/net/keepalive/keepalive.go`（双向空闲检测） | ❌ **目录不存在** | 仅有 `SetKeepAlivePeriod(30s)`（`acceptor.go:120`、`connector.go:33`），即裸 TCP keepalive，非应用层心跳 |
| "ws 传输层取消注释，socket 与 ws 并存" | ❌ **未做** | `engine/net/connect/ws/acceptor.go` 仍是 `package wsNetConnect` + 整文件注释，无 gorilla 依赖 |
| "分包/校验中间件 82-94%，日志 93.5%，会话与队列 100%" | ❌ **与实测不符** | 2026-09-13 修复前实测：`engine/log` 80.2%、`engine/net/session` 60.0%、`engine/net/connect/socket` **0%** |
| "109 用例全过" | ❌ | 修复前 HEAD 实测 48 个测试函数 |
| type1 加密发送侧加锁串行化（`sendMu`） | ✅ **确实存在** | `engine/net/middleware/encrypt/type1/base.go` 有 `sendMu`，旧记录此条准确 |
| `len4Data` 有 `MaxFrameSize` 与 uint64 长度防回绕 | ✅ **确实存在** | `len4Data.go:12,36-41` |
| `safeAccept` 兜底 onAccept panic | ✅ **确实存在** | `acceptor.go` |
| 4096 读缓冲跨 Read 的缓冲别名问题已修 | ✅ **确实存在** | `len4Data.go:24-29` 有拷贝与注释 |

结论：旧记录把**一整批未落地的功能**（半关闭、读超时中间件、应用层心跳、ws 解禁）记成已完成，且自身前后矛盾（顶部称 ws"取消注释"，其第 4 节又写"本轮范围限定为 socket，ws 保持注释"）。

## 项目当前的真实状态

### 已完成（2026-09-13）

`c96344c` 仓库卫生与 CI · `09b5ca3` 生命周期/加密/健壮性修复 · `1ecbb1a` 补齐测试覆盖 · `9a03f90` 修一处修复引入的数据竞争 · `1628d26` 重建本状态记录 · 批次1（M-17 队列上限、P-04 哨兵错误、M-03 会话 ID 回绕探测、M-12 `netClient` 终态守卫、L-2 codec 静默截断报错）· 批次2（M-16 发送侧拷贝契约、L-3 两处分包中间件发送侧长度上限、`Codec` 所有权契约文档化）。

- 全部 **7 个 Critical** 与 8 个 High 中的 5 个已修；批次1/2 又清掉 7 个 Medium/Low
- 覆盖率 74.7% → 88.2%（语句，`-coverpkg=./...`），测试函数 48 → 233
- `go build` / `go vet` / `gofmt -l .` / `go test -race` 连跑 23/23 包全绿；真实二进制端到端冒烟通过
- 注：**`TCP_NODELAY` 一项经核实无需处理**——Go 标准库 `newTCPConn` 默认就调 `setNoDelay(fd, true)`

### 未完成（按优先级）

| 优先级 | 项 |
|---|---|
| **P0** | **DOS-01** 握手阶段无超时无连接数上限（预认证资源耗尽）；`connector.go` 仍是裸 `net.Dial` |
| ~~P0~~ | **Phase 2b 迁移 `crypto/tls`** —— **2026-09-13 决定暂不处理**。后果：CRYPT-03（Key1 由源码公开置换表保护）、M-07（verifier 对字节置换不变）、M-08（帧长明文）、M-09（无 transcript 绑定/可重放/无前向保密）**继续存在** |
| P1 | `log` 包轮转无锁（fd 泄漏/日志丢失/竞态）；DOS-02 `OpenClient` 同步拨号头阻塞；DOS-03/M-02 无每隧道上限与反向内网放大 |
| P2 | M-04 半关闭（需新增 `Conn.CloseWrite`，**目前不存在**）；M-14 后半（慢 `onAccept` 头阻塞 accept 循环） |
| P3 | L-10/L-11、E-06 改名、M-22 剩余（token 自描述编码 + 协议版本字节）、Phase 4 性能项（写合并等；~~TCP_NODELAY~~ 经核实 Go 默认已启用，无需处理）、ws/ 注释代码清理、CI lint 转阻塞 |

**应用层心跳仍未做**，且执行顺序有硬约束：**心跳必须等 type1 握手末帧纳入 `sendMu`（已做）之后再加**，否则会踩"应用数据抢在 ScNo 之前入队 → 永久失步"那个窗口。

## 维护本文件的约定

- 只写**已验证为真**的状态，每条附代码位置或可复跑的命令。
- 任何"已完成"声明都要能在当前 commit 上被 `grep`/`go test` 复核；做不到就写进"未完成"。
- 状态快照若与 `reports/2026-09-13-*.md` 冲突，以后者与代码为准。
