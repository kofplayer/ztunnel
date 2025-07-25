[简体中文文档](ReadmeCh.md)

# ZTunnel

ZTunnel is a NAT traversal tool that supports TCP protocol with zero dependencies and encryption capabilities.

## Overview

- Uses client-server architecture. Requires a server with public IP address.
- Server is deployed on public network to accept client connections.
- Client is deployed on private network, connecting to both server and the service to be exposed.
- One server can handle multiple clients, exposing multiple services simultaneously.

## Usage Guide

Here's an example of exposing a private MySQL service to public network. Assuming server runs on Linux and client runs on Windows.

### Prerequisites:
- Private MySQL service address: 192.168.0.100:3306
- Public server address: 123.51.79.101. Port to expose: 3307

### Steps
- Download the latest Linux x64 server binary [ztunnel_server_linux_x64](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_server_linux_x64) and Windows x64 client binary [ztunnel_client_windows_x64.exe](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_client_windows_x64.exe)
- Copy the server executable `ztunnel_server_linux_x64` to your public server and set execution permissions:
```sh
chmod +x ./ztunnel_server_linux_x64
```
- Start the server. Port 8888 is for client connections, not for end-user connections. Ensure port 8888 is open:
```sh
./ztunnel_server_linux_x64 -listen=8888
```
- Copy `ztunnel_client_windows_x64.exe` to any machine in your private network that can access both the public server and MySQL service. Start it with:
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306
```
- The client will connect to the server and expose MySQL to the public network. Ensure port 3307 is open on the server. MySQL can now be accessed via 123.51.79.101:3307.
- To expose a private GitLab service at 192.168.0.99:30080 to public address 123.51.79.101:30080, run this command on the private network. Ensure port 30080 is open on the server:
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=30080 -forward=192.168.0.99:30080
```

## Message Encryption
Communication between server and client can be encrypted using a custom SSL-like encryption method. Both server and client executables support command line parameters viewable with the -h flag.

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

Using the MySQL example above, to enable encryption with key "mytesttoken", add `-net_encrypt=true -token=mytesttoken` to both server and client commands:

Server:
```sh
./ztunnel_server_linux_x64 -listen=8888 -net_encrypt=true -token=mytesttoken
```

Client:
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306 -net_encrypt=true -token=mytesttoken
```