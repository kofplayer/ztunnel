[简体中文文档](ReadmeCh.md)

# ZTunnel

ZTunnel is a NAT traversal tool that supports TCP protocol, has zero dependencies, and provides encryption functionality.

## Description

- Uses a server-client architecture. Requires one server with public IP address.
- Server is deployed on public network, providing connection points for clients.
- Client is deployed on private network, connecting to both server and the service to be exposed
- One server can handle multiple clients, exposing multiple services to public network simultaneously

## Usage Guide

Here's an example of exposing a private MySQL service to public network. Assuming server runs on Linux and client runs on Windows.

### Environment Setup:
- Private MySQL service address: 192.168.0.100:3306
- Public server address: 123.51.79.101. Port to expose: 3307

### Steps
- Clone the project
- Compile server executable
```sh
go build ./cmd/server
```
- Compile client executable
```sh
go build .\cmd\client
```
- Copy server executable to public server and set execution permission
```sh
chmod +x ./server
```
- Start the server. Port 8888 is for client connections, not for user connections. Ensure port 8888 is open.
```sh
./server -listen=8888
```
- Copy client.exe to any machine in private network that can access both public server and MySQL service. Then start it:
```sh
client.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306
```
- The client will connect to server and expose MySQL to public network. Ensure port 3307 is open on server. Now MySQL can be accessed via 123.51.79.101:3307.
- To expose a private GitLab service at 192.168.0.99:30080 to public address 123.51.79.101:30080, run this command on private network. Ensure port 30080 is open on server:
```sh
client.exe -server=123.51.79.101:8888 -export_port=30080 -forward=192.168.0.99:30080
```

## Message Encryption
Messages between server and client can be encrypted using a custom SSL-like encryption method. Both server and client executables support command line parameters viewable with -h flag.

```sh
PS C:\git_work\kofplayer\ztunnel> .\server.exe -h
Usage of C:\git_work\kofplayer\ztunnel\server.exe:
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
PS C:\git_work\kofplayer\ztunnel> .\client.exe -h
Usage of C:\git_work\kofplayer\ztunnel\client.exe:
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

Using the MySQL example above, to enable encryption with key "mytesttoken", add `-net_encrypt=true -token=mytesttoken` to both server and client commands.

Server:
```sh
./server -listen=8888 -net_encrypt=true -token=mytesttoken
```

Client:
```sh
client.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306 -net_encrypt=true -token