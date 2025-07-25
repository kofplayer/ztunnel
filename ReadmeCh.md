# ZTunnel

ZTunnel是一个内网穿透工具。支持tcp协议，零依赖，提供加密功能。

## 说明

- 使用服务端和客户端的设计。需要一台外网服务器。
- 服务端部署到外网，提供给用户连接。
- 客户端部署到内网，会同时连接服务端和要暴露给外网的服务
- 一个服务端可以连接多个客户端，同时暴露多个服务到外网

## 使用方法

这里以内网mysql服务暴露到外网为例。服务端假设是linux环境，客户端假设是windows环境。

### 假设环境：
- 内网的mysql服务地址是192.168.0.100:3306
- 外网服务器地址为123.51.79.101。需要暴露的端口是3307。

### 操作步骤
- 下载最新的linux_x64服务端执行文件 [ztunnel_server_linux_x64](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_server_linux_x64) 和 windows_x64客户端执行文件 [ztunnel_client_windows_x64.exe](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_client_windows_x64.exe)
- 把可执行文件 ztunnel_server_linux_x64 拷贝到外网服务器上，并设置可执行权限。
```sh
chmod +x ./ztunnel_server_linux_x64
```
- 启动服务器。下面的8888是服务段接收客户端连接的端口，不是给用户连接的端口。请确保8888端口开放。
```sh
./ztunnel_server_linux_x64 -listen=8888
```
- 把可执行文件 ztunnel_client_windows_x64.exe 拷贝到内网任意一台机器上，要求是这台机器可以同时连接外网服务器和内网的mysql服务。并启动。
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306
```
- 这时客户端会连接服务端，并暴露mysql到外网。请确保服务器的3307端口开放。现在可以使用 123.51.79.101:3307 这个地址连接mysql了。
- 这时如果要把内网 192.168.0.99:30080 的gitlab服务暴露到外网 123.51.79.101:30080，那只需要在内网机器上执行下面命令就可以了。请确保服务器的30080端口开放。
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=30080 -forward=192.168.0.99:30080
```

## 消息加密
服务端和客户端之间的消息可以加密。加密方式是自己实现的类似ssl的加密方式。服务端可执行程序和客户端可执行程序都可以使用-h的方式查看命令行参数。

```sh
./ztunnel_server_linux_x64 -h
Usage of ztunnel_server_linux_x64:
  -listen string
        server listen address (default ":8888")
  -log_level int
        log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)
  -net_encrypt
        encrypt data between client and server (default false)
  -token string
        client connect to server token
```

```sh
ztunnel_client_windows_x64.exe -h
Usage ztunnel_client_windows_x64.exe:
  -export_port int
        server export port (default 9999)
  -forward string
        forward address (default "localhost:9999")
  -log_level int
        log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)
  -net_encrypt
        encrypt data between client and server (default false)
  -server string
        server address (default "localhost:8888")
  -token string
        client connect to server token
```

还是以上面暴露mysql到外网为例，加密密钥为 mytesttoken。服务端和客户端启动命令都加上  -net_encrypt=true -token=mytesttoken 就可以了。

服务端：
```sh
./ztunnel_server_linux_x64 -listen=8888 -net_encrypt=true -token=mytesttoken
```

客户端：
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306 -net_encrypt=true -token=mytesttoken
```