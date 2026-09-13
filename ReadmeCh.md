[English documentation](Readme.md)

# ZTunnel

ZTunnel 是一个内网穿透工具。支持 tcp 协议，零第三方依赖，并提供可选的流量混淆。

## 说明

- 使用服务端和客户端的设计。需要一台外网服务器。
- 服务端部署到外网，提供给用户连接。
- 客户端部署到内网，会同时连接服务端和要暴露给外网的服务
- 一个服务端可以连接多个客户端，同时暴露多个服务到外网

## 安全须知（暴露服务前请先读完）

- **`-token` 默认为空，空即完全无鉴权。** 任何能连上控制端口的程序都可以要求服务端监听任意
  端口。两端都必须传强 token。token 为空时启动日志会打印 `SECURITY:` 告警。
- **没有端口白名单，也没有隧道申请限速。** 即使 token 正确，客户端仍可要求监听任意端口，
  因此建议专用主机部署。
- **`-net_encrypt` 是混淆，不是加密。** 自研握手没有服务端身份认证（中间人可拿下会话密钥），
  密钥派生也存在缺陷。请不要依赖它保护机密——应当让被穿透的服务自带 TLS
  （MySQL/Postgres/HTTPS），由它来保证机密性。
- **没有应用层心跳。** NAT 映射失效后隧道可能长期半开而双方无感知。
- 日志写在**相对路径** `./log`（由启动时的工作目录决定落盘点），按小时切分，
  **无大小上限、无自动清理**。

## 使用方法

这里以内网 mysql 服务暴露到外网为例。服务端假设是 linux 环境，客户端假设是 windows 环境。

### 假设环境：
- 内网的 mysql 服务地址是 192.168.0.100:3306
- 外网服务器地址为 123.51.79.101。需要暴露的端口是 3307。

### 操作步骤
- 下载最新的 linux_x64 服务端执行文件 [ztunnel_server_linux_x64](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_server_linux_x64) 和 windows_x64 客户端执行文件 [ztunnel_client_windows_x64.exe](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_client_windows_x64.exe)。也可自行构建：`cd cmd && ./build.bat`，产物落在仓库根目录。
- 把可执行文件 ztunnel_server_linux_x64 拷贝到外网服务器上，并设置可执行权限。
```sh
chmod +x ./ztunnel_server_linux_x64
```
- 启动服务端。下面的 8888 是服务端接收客户端连接的端口，不是给用户连接的端口。请确保 8888 端口开放。
```sh
./ztunnel_server_linux_x64 -listen=:8888 -token=mytesttoken
```
  > `-listen` 必须是 `host:port` 形式——**是 `:8888` 而不是 `8888`**。缺冒号会被判定为非法
  > 地址，进程以退出码 1 退出。只想本机可达就写 `127.0.0.1:8888`，IPv6 写 `[::1]:8888`。
- 把可执行文件 ztunnel_client_windows_x64.exe 拷贝到内网任意一台机器上，要求是这台机器可以同时连接外网服务器和内网的 mysql 服务。并启动。
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306 -token=mytesttoken
```
- 这时客户端会连接服务端，并暴露 mysql 到外网。请确保服务器的 3307 端口开放。现在可以使用 123.51.79.101:3307 这个地址连接 mysql 了。
- 这时如果要把内网 192.168.0.99:30080 的 gitlab 服务暴露到外网 123.51.79.101:30080，那只需要在内网机器上执行下面命令就可以了。请确保服务器的 30080 端口开放。
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=30080 -forward=192.168.0.99:30080 -token=mytesttoken
```

### 重连行为

客户端是**永久重连**循环：任何一次断开或建隧道失败，都会停下来、**等待 10 秒**后重新连接。
它不会自行退出，所以请用进程管理器托管，不要把"进程还在"当成"隧道正常"。

## 命令行参数

两个可执行文件都可以用 `-h` 查看参数。

```sh
./ztunnel_server_linux_x64 -h
Usage of ./ztunnel_server_linux_x64:
  -export_ip string
        bind address for tunnel export ports (empty = all interfaces)
  -listen string
        server listen address (host:port, e.g. 127.0.0.1:8888) (default ":8888")
  -log_level int
        log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)
  -net_encrypt
        encrypt data between client and server (default false)
  -token string
        client connect to server token
```

| 参数 | 含义 |
|---|---|
| `-listen` | 控制通道的绑定地址。**host 部分会真正生效**；host 为空表示所有网卡。 |
| `-export_ip` | 每条隧道对外暴露端口（终端用户连接的那些）的绑定地址。为空表示所有网卡。可用它把暴露的服务固定到指定网卡。 |
| `-token` | 必须与客户端一致。为空即无鉴权。 |
| `-net_encrypt` | 必须与客户端一致。见上文安全须知。 |
| `-log_level` | 0=DEBUG 1=INFO 2=WARN 3=ERROR 4=FATAL 5=NONE |

```sh
ztunnel_client_windows_x64.exe -h
Usage of ztunnel_client_windows_x64.exe:
  -export_port uint
        server export port (1-65535) (default 9999)
  -forward string
        forward address (host:port) (default "localhost:9999")
  -log_level int
        log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)
  -net_encrypt
        encrypt data between client and server (default false)
  -server string
        server address (host:port) (default "localhost:8888")
  -token string
        client connect to server token
```

| 参数 | 含义 |
|---|---|
| `-server` | 服务端控制通道地址。 |
| `-export_port` | 要求服务端在公网监听的端口。启动时校验 `1-65535`；越界会直接报错退出，不再被静默截断成另一个端口。 |
| `-forward` | 要暴露的内网服务地址。**服务端无法指定这个地址**——它只来自本参数，所以被攻陷的服务端不能借此让客户端去连别的目标。 |
| `-token` / `-net_encrypt` | 必须与服务端一致。 |

## 半关闭

任意一端发出 FIN，ZTunnel 就会**同时拆除两个方向**。因此"写完请求再关写侧、然后读响应"的
协议（HTTP/1.0 的 `Connection: close`、`nc`、SMTP/FTP 的 `QUIT` 流程）会看到响应被截断。
MySQL/Postgres 这类长连接空闲场景不受影响。
