# engine/net/middleware/ — 中间件实现（L2）

> 上级：[../CLAUDE.md](../CLAUDE.md)（链机制与数据流向在此，不重复）。各实现细节本文。栈配置规则见 [../../../common/CLAUDE.md](../../../common/CLAUDE.md)。

## middleware.go — 基础设施

- `Middleware` 接口：`SetPre/SetNext/Pre/Next/FireEvent/OnEvent/ReceiveData/SendData`。
- `MiddlewareBase`：默认 `ReceiveData → Next`、`SendData → Pre`（即默认透传，子类只覆写关心的方向）；`First()` 沿 Pre 链回溯到链头。
- `CreateMiddlewareFunc = func() Middleware`：**工厂签名，保证每连接独立实例**（server 侧必需；client 侧直接传 `NewXxx` 函数值即可）。

## common/ — 链端点

- `NetMiddlewareFirst`（`first.go`）：链头。`SendData` 直连 socket 发送函数；`FireEvent` 带重入事件队列且**线程安全**（OnConnect 出自拨号/accept goroutine，OnReady/OnDisconnect 出自接收 goroutine，可并发；firing 期间新事件排队，`OnEvent` 在锁外执行防重入死锁）。
  - ⚠️ `firingEvent` 的复位**必须放在 defer 里**：`OnEvent` 可能 panic（type1 的状态守卫就是显式 panic），否则该标记永远停在 true、而执行它的 goroutine 已消失 → 此后这条链上所有事件（含 `OnDisconnect`）只入队、永不派发（报告 CRASH-03）。已排队事件保留，由下一次派发一起排空。
- ⚠️ `MiddlewareBase.FireEvent` 委托链头，但带一条守卫：**链头是自己时（`pre == nil`）就地派发 `OnEvent`**。缺它时 `m.First().FireEvent(e)` 会重新进入同一方法 → 无限自我递归 → **stack overflow**；而 Go 的栈溢出是 fatal error、`recover` 拦不住，会绕过 `recoverPanic`/`safeAccept` 的全部兜底（报告 CRASH-04）。因此**不要在未接完整的链上调用 `FireEvent`**。
- `NetMiddlewareLast`（`last.go`）：链尾。`ReceiveData` 调 codec.Decode + 业务 onMessage；收到 `OnReady` 事件时调业务 `onReady`（client 的 `Connect()` 靠它解除阻塞，见 [../CLAUDE.md](../CLAUDE.md)）。

## package/ — TCP 分包（流→消息）

TCP 是字节流，靠分包中间件恢复消息边界；内部持有残包缓冲（跨 Read 拼包）：

| 实现 | 帧格式 | 用途 |
|---|---|---|
| `len4Data` | `len(4B BE) + data` | **控制通道使用**（len 只算 data 长度） |
| `len2Data` | `len(2B BE) + data` | 未使用（备用，上限 64KB） |
| `fullData` | 无帧，透传 | **数据通道使用**（代理转发的是无边界的原始流） |

**len4Data 的安全约束**（长度前缀是攻击者可控输入）：
- `MaxFrameSize = 8MB`：超限立即报错，不接受超长声明（否则缓慢喂包可致内存耗尽）。
- 长度用 **uint64** 计算：uint32 下 `0xFFFFFFFF+4` 回绕为 3 会触发切片越界 panic。
- **接收数据一律拷贝**进自有缓冲：调用方（receiverRun）复用读取缓冲，直接别名会让已转发给下游（进入发送队列）的帧被下一次读取覆盖（静默数据损坏）。残包耗尽后释放底层缓冲，避免长连接长期持有峰值容量。
- len2Data 采用相同拷贝策略（uint16 无回绕风险）。

## encrypt/type0/ — 空加密（关键伏笔）

`ClientNetEncrypt`/`ServerNetEncrypt` **完全不加解密**，唯一作用：收到 `OnConnect` 事件时 `FireEvent(OnReady)`，让链提前进入就绪态。

为什么需要它：`NetClient.Connect()` 统一等待 OnReady（见 [../CLAUDE.md](../CLAUDE.md)）；type1 有真实握手，type0 用空实现补齐「无加密也要发 OnReady」的对称性。**明文模式的栈里它是必需品，不是可删项。**

注意：type0 的 server 侧提供的是 `CreateServerNetEncryptFunc()` 工厂形式，client 侧是 `NewClientNetEncrypt` 直接函数值——与 type1 保持同样形态。

## encrypt/type1/ — SSL 风格加密握手

**密钥材料**：`Key = uint64`（8 字节）；固定 256 字节替换表 `enTable/deTable`（互逆置换；⚠️ 该表在发行源码中公开，对 Key1 **不提供任何机密性**，见下文 CRYPT-03）；RSA-2048（PKIX 公钥、OAEP-SHA256）。

**握手流程**（状态机，client 的 `ClientStatus*` / server 的 `ServerStatus*`）：

1. **client OnConnect**：生成 `Key1` → **先置状态 `WaitKey2AndPK` 再发送**（发送是阻塞调用，对端应答可能在调用返回前到达接收 goroutine）→ 查表加密 8B → 发送。
2. **server WaitKey1**：解表得 Key1 → 生成 `Key2` → 发送 `[Key2低4B | RSA公钥 | Key2高4B]`（`getKeyAndStrBytes` 布局），整体用 Key1 做流掩码。
3. **client WaitKey2AndPK**：Key1 解掩码 → 解析 Key2 与公钥 → 生成 `CsNo`（client→server 包序号密钥）→ RSA 加密后发送。
4. **server WaitCsNo**：RSA 解出 CsNo → 生成 `ScNo`（server→client 方向）→ 用 `Key1^Key2^CsNo` 掩码后**发送 ScNo（必须在 FireEvent 之前，对端要靠它完成握手）** → **FireEvent(OnReady)**。
5. **client WaitScNo**：解掩码得 ScNo → **FireEvent(OnReady)**（client 侧 Connect() 至此返回）。

**并发模型**（`-race` 驱动的修复，改动时必须维持）：

- `mu`：互斥**握手状态机**——`OnEvent`（拨号 goroutine）与 `ReceiveData`（接收 goroutine）并发读写 `status`/密钥字段。锁内只做状态与密钥变更；RSA 运算、阻塞发送、下游分发（`Next().ReceiveData`/`FireEvent`）一律锁外——onReady 回调可能同步回发消息，持锁跨过 SendData 会重入死锁。
- `sendMu`：串行化 **SendData**——控制会话被多个 goroutine 并发 SendMessage（每个用户连接各自的 receiver goroutine 都会向它发消息），`GoNextCsNo/GoNextScNo` 与掩码加密必须原子执行，否则掩码序列错乱、对端永久失步。

**后续流量加密**：纯 XOR 流掩码——`data[i] ^= mask[i%8]`，其中 mask 字节来自 `Key1^Key2^CsNo`（c→s）或 `Key1^Key2^ScNo`（s→c）；**每个包收发后序号密钥演化**（`GoNextCsNo/GoNextScNo`：`no += (keyA-keyB) ^ no`），双方同步演化故可解。

**server 侧密钥对**：`CreateServerNetEncryptFunc()` 在**创建工厂时**生成一次 RSA-2048 密钥对，所有连接共用（性能考虑；也意味着全部连接共享同一 RSA 密钥）。

⚠️ **实际安全强度（2026-09-13 实测修正）**：本层此前被描述为"仅能对抗被动窃听"，该结论**已撤销**——三处独立缺陷叠加后，**被动观察者即可解密整条会话**：

1. **掩码移位缺陷（CRYPT-01）**：`KeyMaskData` 的掩码字节按位而非按字节取，8 字节掩码只覆盖 key 的 bit0..bit14，**高 49 bit 被丢弃**。实测 64 位 key 空间只产生 **32768 种**掩码；且低 15 bit 为 0 的 key 产生**全零掩码**（概率 1/32768），该连接数据原样明文上网。证明见 `reports/evidence/CRYPT-01_mask_entropy.go`。
2. **密钥源非 CSPRNG（CRYPT-02）**：`Key1/Key2/CsNo/ScNo` 全部由 `math/rand` 生成，而它是非线性密码学的发生器，不具备不可预测性。现改用 `crypto/rand`；`GenKey()` 失败必须以 error 上抛，**绝不降级返回 0**（Key=0 → 掩码全零 → 明文）。
3. **Key1 等同明文（CRYPT-03）**：首帧只靠 `enTable` 保护，而该表是**发行源码里的公开常量**，逐字节查 `deTable` 即零成本还原 Key1 全 64 bit。且握手帧经 `m.Pre().SendData` **绕开 verifier**（栈序 `len4 → type1 → verifier`，校验字节在加密层之内），首帧无完整性保护、无 transcript 绑定——改一位首帧即等于攻击者指定 Key1。

因此：**不要依赖 `-net_encrypt` 保护机密**。被穿透的服务应自带 TLS（MySQL/Postgres/HTTPS），由服务层保证机密性。根治方案仍是迁移 `crypto/tls` + 自签证书 + 公钥指纹 pinning（标准库，满足零依赖）。

已加固项（2026-09-13）：RSA 位数 1024 → **2048**（1024 恰好踩在 `rsa.EncryptOAEP` 的隐式下限上，Go 一旦上调就会全线握手失败）；`GetPublicKeyByBytes` 新增显式对端策略 `minRSABits=2048 / minRSAExponent=65537`（`x509.ParsePKIXPublicKey` 本身接受弱公钥）；client 两个握手分支补齐长度校验（原先短帧会被静默补零成合法密钥）；握手末帧（ScNo）已纳入 **sendMu 串行域**且 `status=HandsFinish` 的翻转也在该串行域内，消除"应用数据抢在 ScNo 之前入队 → 永久失步"的窗口（锁序约定：**sendMu → mu**，切勿持 mu 再取 sendMu）。

⚠️ **断代说明**：CRYPT-01 的掩码修正与公钥策略下限都会改变线上行为，**两端必须升级到同一版本**，新旧混跑握手必然失败。改动加密相关代码时仍**不要破坏握手报文的字节布局**（步骤 2/4 的掩码字段顺序）。协议至今**无版本字段**，任何格式变更都是断代。

⚠️ 其它仍成立的取舍：**无服务端身份验证**（客户端接受任意公钥，主动 MITM 可代理两侧握手并拿到 token）；XOR 流掩码无 MAC，**verifier 的 XOR 校验和对字节置换完全不变**（重排 payload 即 100% 绕过，属"防错不防伪"）；帧长在最外层明文，精确长度/消息边界/时序外泄；服务端 RSA 私钥全连接共用 → **无前向保密**，私钥泄露后全部历史抓包可一次解密。

## verifier/ — XOR 校验

仅用于加密模式（栈序：`len4 → type1 → verifier`，即校验字节在加密层之内）。算法：checksum = `len(data)&0xFF` 逐字节 XOR；发送追加 1 字节，接收校验整体 XOR 为 0 后剥除。**防错不防伪**（无 MAC），配合加密层提供基本完整性。

## 新增自定义中间件的检查单

1. 内嵌 `netMiddleware.MiddlewareBase`，只覆写需要的方向（`ReceiveData`/`SendData`）与 `OnEvent`。
2. 若有状态（缓冲/序号），确认实例是**每连接一份**（server 侧必须走 `CreateMiddlewareFunc` 工厂）。
3. **并发模型先行**：想清楚每个回调运行在哪个 goroutine、哪些字段被跨 goroutine 读写——握手状态机与发送序号要分开加锁（参考 type1 的 `mu`/`sendMu` 分工），锁内不得调用会重入本中间件的阻塞操作。
4. 放在链中的位置决定语义：靠 First = 传输层职责（分包），靠 Last = 应用层职责（校验/业务编码）。
5. 需要就绪信号（如自定义握手）时，完成时 `FireEvent(MiddlewareEventOnReady)`，参考 type1。
