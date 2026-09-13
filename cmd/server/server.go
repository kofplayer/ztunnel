package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"ztunnel/common/proto"
	"ztunnel/common/util"
	"ztunnel/engine/log"
	"ztunnel/server/inserver"
)

var (
	log_level   = flag.Int("log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	listen      = flag.String("listen", ":8888", "server listen address (host:port, e.g. 127.0.0.1:8888)")
	export_ip   = flag.String("export_ip", "", "bind address for tunnel export ports (empty = all interfaces)")
	token       = flag.String("token", "", "client connect to server token")
	net_encrypt = flag.Bool("net_encrypt", false, "encrypt data between client and server (default false)")
)

// run 把全部失败路径收敛成一个 error，由 main 统一以非 0 退出码结束。
// 此前配置错误只记一条日志就 return，进程以退出码 0 结束——systemd/Docker
// 的 Restart= 策略会认为"运行正常"（报告 M-19 / ROBUST-02）。
func run() error {
	flag.Parse()

	if *log_level < log.DEBUG || *log_level > log.NONE {
		return fmt.Errorf("invalid -log_level %d (must be %d-%d)", *log_level, log.DEBUG, log.NONE)
	}
	host, port, err := util.GetHostAndPort(*listen)
	if err != nil {
		return fmt.Errorf("invalid -listen: %w", err)
	}
	exportIP, err := splitOptionalIP(*export_ip)
	if err != nil {
		return fmt.Errorf("invalid -export_ip: %w", err)
	}

	proto.SetToken(*token)
	proto.NetEncrypt = *net_encrypt

	logger := log.NewLog()
	// Init 失败必须中止：否则 logerAll 为 nil，后续任一日志调用都会 panic，
	// 而 panic 可能发生在连接收发循环的 recover 兜底路径上（报告 CRASH-02）。
	if err := logger.Init("./log", "zs_"); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger.SetLogLevel(int32(*log_level))
	log.SetMainLog(logger)

	if *token == "" {
		log.Main().Error("SECURITY: -token is empty, ANY client may open a tunnel and " +
			"make this server listen on an arbitrary port. Set a strong token for public deployment.")
	}
	if !*net_encrypt {
		log.Main().Warn("-net_encrypt is false: the control channel (including the token) " +
			"travels in cleartext")
	}

	log.Main().Info("start server on %v, export ports bind to %q",
		net.JoinHostPort(host, strconv.Itoa(int(port))), exportIP)

	if err := inserver.NewServer(host, port, exportIP).Start(); err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	return nil
}

// splitOptionalIP 接受空串（通配）或裸 IP / 主机名；带端口的写法一律拒绝，
// 避免 -export_ip 与 -listen 的格式被混淆。
func splitOptionalIP(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return "", fmt.Errorf("%q must be an address without port (e.g. 192.0.2.1)", s)
	}
	if ip := net.ParseIP(s); ip == nil {
		return "", fmt.Errorf("%q is not a valid IP address", s)
	}
	return s, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ztunnel server: %v\n", err)
		os.Exit(1)
	}
}
