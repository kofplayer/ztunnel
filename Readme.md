[简体中文文档](ReadmeCh.md)

# ZTunnel

ZTunnel is a NAT traversal tool that supports TCP protocol with zero dependencies.

## Overview

- Uses client-server architecture. Requires a server with public IP address.
- Server is deployed on public network to accept client connections.
- Client is deployed on private network, connecting to both server and the service to be exposed.
- One server can handle multiple clients, exposing multiple services simultaneously.

## Security — read this before exposing anything

- **An empty `-token` (the default) means no authentication at all.** Any host that can reach
  the control port can ask the server to listen on an arbitrary port. Always pass a strong
  `-token` to both sides. The startup log prints a `SECURITY:` warning when it is empty.
- **There is no port allow-list and no rate limit on tunnel requests.** With a valid token a
  client may still request any port, so run this on a dedicated host.
- **`-net_encrypt` is obfuscation, not encryption.** The custom handshake has no server
  authentication, so an active man-in-the-middle can recover the session key; the built-in key
  derivation also has a defect that collapses per-packet key entropy. Do not rely on it to
  protect secrets — tunnel a service that has its own TLS (MySQL/Postgres/HTTPS) instead.
- **No application-layer heartbeat.** A tunnel can stay half-open indefinitely after a NAT
  mapping expires.
- Logs are written to the **relative** path `./log` (so the working directory decides where),
  rotated hourly, with **no size cap and no cleanup**.

## Usage Guide

Here's an example of exposing a private MySQL service to public network. Assuming server runs on Linux and client runs on Windows.

### Prerequisites:
- Private MySQL service address: 192.168.0.100:3306
- Public server address: 123.51.79.101. Port to expose: 3307

### Steps
- Download the latest Linux x64 server binary [ztunnel_server_linux_x64](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_server_linux_x64) and Windows x64 client binary [ztunnel_client_windows_x64.exe](https://github.com/kofplayer/ztunnel/releases/download/v0.1.0/ztunnel_client_windows_x64.exe). Building from source is also a single command: `cd cmd && ./build.bat` (artifacts land in the repository root).
- Copy the server executable `ztunnel_server_linux_x64` to your public server and set execution permissions:
```sh
chmod +x ./ztunnel_server_linux_x64
```
- Start the server. Port 8888 is for client connections, not for end-user connections. Ensure port 8888 is open:
```sh
./ztunnel_server_linux_x64 -listen=:8888 -token=mytesttoken
```
  > `-listen` requires the `host:port` form — **`:8888`, not `8888`**. A value without a colon
  > is rejected and the process exits with status 1. Use `127.0.0.1:8888` to keep the control
  > port local-only, and IPv6 as `[::1]:8888`.
- Copy `ztunnel_client_windows_x64.exe` to any machine in your private network that can access both the public server and MySQL service. Start it with:
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=3307 -forward=192.168.0.100:3306 -token=mytesttoken
```
- The client will connect to the server and expose MySQL to the public network. Ensure port 3307 is open on the server. MySQL can now be accessed via 123.51.79.101:3307.
- To expose a private GitLab service at 192.168.0.99:30080 to public address 123.51.79.101:30080, run this command on the private network. Ensure port 30080 is open on the server:
```sh
ztunnel_client_windows_x64.exe -server=123.51.79.101:8888 -export_port=30080 -forward=192.168.0.99:30080 -token=mytesttoken
```

### Reconnect behaviour

The client loops forever: on any disconnect or failed tunnel request it stops, **waits 10
seconds**, and reconnects. It never exits on its own, so supervise it with a process manager
rather than assuming a clean exit code means the tunnel is down.

## Command line reference

Both executables print their flags with `-h`.

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

| flag | meaning |
|---|---|
| `-listen` | Control-channel bind address. The host part **is** honoured; empty host means all interfaces. |
| `-export_ip` | Bind address for the public ports opened per tunnel (the ones end users connect to). Empty means all interfaces. Set it to pin exposed services to a specific NIC. |
| `-token` | Must match the client. Empty = no authentication. |
| `-net_encrypt` | Must match the client. See the caveat above. |
| `-log_level` | 0=DEBUG … 5=NONE |

```sh
ztunnel_client_windows_x64.exe -h
Usage of ztunnel_client_windows_x64.exe:
  -dial_timeout duration
        TCP dial timeout (must be > 0) (default 5s)
  -export_port uint
        server export port (1-65535) (default 9999)
  -forward string
        forward address (host:port) (default "localhost:9999")
  -handshake_timeout duration
        handshake completion timeout (must be > 0) (default 30s)
  -log_level int
        log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)
  -net_encrypt
        encrypt data between client and server (default false)
  -server string
        server address (host:port) (default "localhost:8888")
  -token string
        client connect to server token
```

| flag | meaning |
|---|---|
| `-server` | Control-channel address of the server. |
| `-export_port` | Public port the server should listen on. Validated to `1-65535`; an out-of-range value now fails at startup instead of being silently truncated to another port. |
| `-forward` | The private service to expose. **The server cannot choose this** — it only comes from this flag, so a compromised server cannot make the client connect elsewhere by itself. |
| `-dial_timeout` / `-handshake_timeout` | Must be positive. Before these existed, dialing used a bare `net.Dial` with no timeout (a firewalled target stalled for ~21 s at the kernel default), and waiting for the handshake had **no timeout at all**: a peer that accepted the TCP connection but never spoke would block `Connect()` forever, leaking its goroutines and the connection while the process looked healthy. |
| `-token` / `-net_encrypt` | Must match the server. |

## Half-closed connections

ZTunnel tears down **both** directions as soon as either side sends FIN. Protocols that write a
request, shut down their write side, and then read the response (HTTP/1.0 `Connection: close`,
`nc`, SMTP/FTP `QUIT` flows) will see the response truncated. Long-lived idle connections to
MySQL/Postgres are fine.
