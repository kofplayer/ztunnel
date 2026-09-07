# testutil/ — 测试辅助库（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。零依赖测试辅助，**仅供测试代码引用**（零依赖硬性约束同样适用于测试）。各包 `*_test.go` 是用例本体（回归用例名与历史问题编号对应）。

## 运行

```sh
go test ./... -count=1                    # 全量回归
CGO_ENABLED=1 PATH="/c/mingw64/bin:$PATH" go test -race ./... -count=1   # 数据竞争全量检测
```

- `-race` 依赖 gcc：本机工具链**实体拷贝**在 `C:\mingw64`（WinLibs 原始路径含空格，gcc 内部调 ld 会截断路径；junction 也会被 gcc 解析回真实路径，必须实体拷贝。WinLibs 升级后需重新拷贝）。
- `RaceEnabled`（`race_enabled.go`/`race_disabled.go` 双构建标签）供用例区分 race 构建。

## 组件

| 文件 | 内容 |
|---|---|
| `assert.go` | `NoError/Error/Equal/BytesEqual/True` + **`Eventually`**（50ms 轮询——全部异步断言用它，不用 sleep） |
| `stubmw.go` | **`BuildChain`**（按 `netClient.Connect` 的生产逻辑拼装中间件链）、**`Recorder`**（按引用保存帧，用于缓冲别名检测）、**`Pump`**（模拟 receiverRun：读→拷贝→喂链，链错误时关连接）、`SeqPayload/VerifySeqPayload`（带序号完整性载荷） |
| `fakenet.go` | `FakeSession`（NetSession 桩：可控 BindObject 含 nil、记录 SendMessage、Close 计数）、`FakeConn` |
| `port.go` | `FreePort`（:0 试绑取端口；存在 TOCTOU 窗口，失败重试 3 次） |
| `log.go` | `SilentLog`：静音（NONE 级）主日志到临时目录——`logImp.Init` 按 cwd 拼相对路径，因此内部用 `t.Chdir` |
| `crash.go` | 崩溃探针（见下） |

## 崩溃探针模式（"未修复前会 panic"的回归）

```go
func TestX_NoPanic(t *testing.T) {
    if testutil.InCrashProbe() {
        doDanger() // 未修复时在此 panic，子进程非零退出
        return
    }
    if err := testutil.RunCrashProbe(t); err != nil {
        t.Fatalf("回归 #N 未修复: %v", err)
    }
}
```

子进程方式让修复前表现为**受控失败**（父测试红、测试二进制不崩、失败信息带子进程 panic 栈），修复后子进程干净退出、用例自动转绿。

## 约束与坑

- `proto.Token`/`NetEncrypt` 是全局变量 → 同包用例**串行**执行（不开 `t.Parallel`），进用例保存、退出恢复。
- 用 `net.Pipe` 测中间件链时：**写 A 端的数据从 B 端读出**（写 c1 → 从 s1 读），接反会收到自己发的帧——历史上两次踩坑，接错时症状是握手报错/失步。
- `Pump` 内部对每次读**拷贝**字节，用于隔离 len4 缓冲别名问题对加密类测试的干扰；len4 的别名回归用例不走 Pump，直接以调用方改写缓冲的方式验证下游帧不变。
- 端口占用类测试的"占位"监听必须绑 `0.0.0.0`（通配）：Windows 下先绑具体 IP 不影响后续通配绑定，会绕过端口冲突。
