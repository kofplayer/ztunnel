package main

import (
	"errors"
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

// errHelp 表示用户请求了 -h：按 Go flag 包 ExitOnError 的惯例以退出码 0 结束。
var errHelp = errors.New("help requested")

type config struct {
	logLevel   int
	listen     string
	exportIP   string
	token      string
	netEncrypt bool
}

func newFlagSet() (*flag.FlagSet, *config) {
	fs := flag.NewFlagSet(progName(), flag.ContinueOnError)
	c := &config{}
	fs.IntVar(&c.logLevel, "log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	fs.StringVar(&c.listen, "listen", ":8888", "server listen address (host:port, e.g. 127.0.0.1:8888)")
	fs.StringVar(&c.exportIP, "export_ip", "", "bind address for tunnel export ports (empty = all interfaces)")
	fs.StringVar(&c.token, "token", "", "client connect to server token")
	fs.BoolVar(&c.netEncrypt, "net_encrypt", false, "encrypt data between client and server (default false)")
	return fs, c
}

// progName 沿用 flag 包对 CommandLine 的做法：用 os.Args[0] 作为程序名，
// 这样 -h 打印的 "Usage of ..." 与直接调用 flag.Parse() 时完全一致，
// 也不会与 Readme 中记录的输出对不上。
func progName() string {
	if len(os.Args) > 0 {
		return os.Args[0]
	}
	return "ztunnel_server"
}

func parseFlags(args []string) (config, error) {
	fs, c := newFlagSet()
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return *c, errHelp
		}
		return *c, err
	}
	return *c, nil
}

// resolvedAddr 是校验后的监听配置。
type resolvedAddr struct {
	host     string
	port     uint16
	exportIP string
}

// validate 把所有纯校验逻辑集中在这里：不碰文件系统、不起网络，因此可单测。
func (c config) validate() (resolvedAddr, error) {
	if c.logLevel < log.DEBUG || c.logLevel > log.NONE {
		return resolvedAddr{}, fmt.Errorf("invalid -log_level %d (must be %d-%d)", c.logLevel, log.DEBUG, log.NONE)
	}
	host, port, err := util.GetHostAndPort(c.listen)
	if err != nil {
		return resolvedAddr{}, fmt.Errorf("invalid -listen: %w", err)
	}
	exportIP, err := splitOptionalIP(c.exportIP)
	if err != nil {
		return resolvedAddr{}, fmt.Errorf("invalid -export_ip: %w", err)
	}
	return resolvedAddr{host: host, port: port, exportIP: exportIP}, nil
}

// splitOptionalIP 接受空串（通配）或裸 IP；带端口一律拒绝，
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

// run 校验配置、初始化日志并进入监听。所有失败都收敛成一个 error，
// 由 main 以非 0 退出码结束——此前配置错误只记一条日志就 return，进程以
// 退出码 0 结束，systemd/Docker 的 Restart= 策略会认为"运行正常"（M-19）。
func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}
	addr, err := cfg.validate()
	if err != nil {
		return err
	}

	proto.SetToken(cfg.token)
	proto.NetEncrypt = cfg.netEncrypt

	logger := log.NewLog()
	// Init 失败必须中止：否则 logerAll 为 nil，后续任一日志调用都会 panic，
	// 而 panic 可能发生在连接收发循环的 recover 兜底路径上（报告 CRASH-02）。
	if err := logger.Init("./log", "zs_"); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger.SetLogLevel(int32(cfg.logLevel))
	log.SetMainLog(logger)

	if cfg.token == "" {
		log.Main().Error("SECURITY: -token is empty, ANY client may open a tunnel and " +
			"make this server listen on an arbitrary port. Set a strong token for public deployment.")
	}
	if !cfg.netEncrypt {
		log.Main().Warn("-net_encrypt is false: the control channel (including the token) travels in cleartext")
	}

	log.Main().Info("start server on %v, export ports bind to %q",
		net.JoinHostPort(addr.host, strconv.Itoa(int(addr.port))), addr.exportIP)

	if err := inserver.NewServer(addr.host, addr.port, addr.exportIP).Start(); err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "ztunnel server: %v\n", err)
		os.Exit(1)
	}
}
