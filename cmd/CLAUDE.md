# cmd/ — 程序入口（L1）

> 上级：[../CLAUDE.md](../CLAUDE.md)。两个 `package main` 入口，只做参数解析、日志初始化和启动，业务逻辑全部在 [../client/](../client/CLAUDE.md) 与 [../server/](../server/CLAUDE.md)。

## 文件

| 文件 | 说明 |
|---|---|
| `client/client.go` | 客户端入口 → 启动 `outclient` |
| `server/server.go` | 服务端入口 → 启动 `inserver` |
| `build.bat` | Windows 交叉编译脚本 |

## 启动流程（两个入口一致）

1. `flag.Parse()` → `proto.SetToken(token)` + `proto.NetEncrypt = net_encrypt`（全局开关，见 [../common/CLAUDE.md](../common/CLAUDE.md)）
2. 初始化日志：`log.NewLog()` → `Init("./log", 前缀)` → `log.SetMainLog(logger)`
3. `util.GetHostAndPort()` 拆地址（格式必须是 `host:port`，host 可为空）

## Flag 参数表

**client**（日志前缀 `zc_<export_port>_`）：

| flag | 默认值 | 说明 |
|---|---|---|
| `-server` | `localhost:8888` | 服务端控制通道地址 |
| `-export_port` | `9999` | 要求服务端在公网监听的端口 |
| `-forward` | `localhost:9999` | 内网真实服务地址 |
| `-net_encrypt` | `false` | 控制通道加密（两端必须一致） |
| `-token` | `""` | 鉴权令牌（两端必须一致） |
| `-log_level` | `0` | 0=DEBUG 1=INFO 2=WARN 3=ERROR 4=FATAL 5=NONE |

**server**（日志前缀 `zs_`）：`-listen`（默认 `:8888`，客户端连接端口，非最终暴露端口）、`-net_encrypt`、`-token`、`-log_level`。

## 客户端重连循环

`client.go` 的 `main` 是**死循环重连**：`cli.Start()` 阻塞直到断开（Start 内部 `<-c.c` 等待），失败或断开后 sleep 10s 再 `Stop()` 并重建。重连逻辑在这里，不在 outclient 内部。

## build.bat 说明

```bat
SET CGO_ENABLED=0 / GOARCH=amd64，GOOS 在 linux/windows 间切换
```

- 在 `cmd/` 目录下执行：分别进入 `client/`、`server/` 各编译 linux + windows 两个二进制
- ⚠️ **产物位置**：`move server ..\ztunnel_server_linux_x64` 中的 `..` 是 `cmd/`，所以 4 个二进制最终落在 **`cmd/` 目录**而非仓库根目录（Readme 下载链接对应的发布产物是手动搬运的）
- 若新增文件/包，此脚本无需改动（`go build` 自动处理）

## 修改注意

- 两个入口依赖 `proto.Token`/`proto.NetEncrypt` 全局变量，**必须在创建任何 client/server 之前设置**，否则中间件栈选择错误（见 [../common/CLAUDE.md](../common/CLAUDE.md) 的栈选择表）。
