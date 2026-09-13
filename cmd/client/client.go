package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
	"ztunnel/client/outclient"
	"ztunnel/common/proto"
	"ztunnel/common/util"
	"ztunnel/engine/log"
)

// errHelp 表示用户请求了 -h：按 Go flag 包 ExitOnError 的惯例以退出码 0 结束。
var errHelp = errors.New("help requested")

// reconnectDelay 是两次重连之间的间隔。测试里可调小，避免真等 10 秒。
var reconnectDelay = 10 * time.Second

type config struct {
	logLevel   int
	server     string
	forward    string
	exportPort uint
	netEncrypt bool
	token      string
}

func newFlagSet() (*flag.FlagSet, *config) {
	fs := flag.NewFlagSet(progName(), flag.ContinueOnError)
	c := &config{}
	fs.IntVar(&c.logLevel, "log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	fs.StringVar(&c.server, "server", "localhost:8888", "server address (host:port)")
	fs.StringVar(&c.forward, "forward", "localhost:9999", "forward address (host:port)")
	fs.UintVar(&c.exportPort, "export_port", 9999, "server export port (1-65535)")
	fs.BoolVar(&c.netEncrypt, "net_encrypt", false, "encrypt data between client and server (default false)")
	fs.StringVar(&c.token, "token", "", "client connect to server token")
	return fs, c
}

// progName 沿用 flag 包对 CommandLine 的做法：用 os.Args[0] 作为程序名，
// 这样 -h 打印的 "Usage of ..." 与直接调用 flag.Parse() 时完全一致。
func progName() string {
	if len(os.Args) > 0 {
		return os.Args[0]
	}
	return "ztunnel_client"
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

// endpoints 是校验后的地址三元组。
type endpoints struct {
	serverHost  string
	serverPort  uint16
	forwardHost string
	forwardPort uint16
	exportPort  uint16
}

// validate 集中所有纯校验逻辑：不碰文件系统、不联网，因此可单测。
func (c config) validate() (endpoints, error) {
	if c.logLevel < log.DEBUG || c.logLevel > log.NONE {
		return endpoints{}, fmt.Errorf("invalid -log_level %d (must be %d-%d)", c.logLevel, log.DEBUG, log.NONE)
	}
	// 此前是 flag.Int + uint16() 强转且无范围校验：-export_port=70000 会静默
	// 变成 4464（暴露到错误的端口），=65536 变成 0（陷入重连死循环）（M-20）。
	if c.exportPort == 0 || c.exportPort > 65535 {
		return endpoints{}, fmt.Errorf("invalid -export_port %d (must be 1-65535)", c.exportPort)
	}
	serverHost, serverPort, err := util.GetHostAndPort(c.server)
	if err != nil {
		return endpoints{}, fmt.Errorf("invalid -server: %w", err)
	}
	forwardHost, forwardPort, err := util.GetHostAndPort(c.forward)
	if err != nil {
		return endpoints{}, fmt.Errorf("invalid -forward: %w", err)
	}
	return endpoints{
		serverHost: serverHost, serverPort: serverPort,
		forwardHost: forwardHost, forwardPort: forwardPort,
		exportPort: uint16(c.exportPort),
	}, nil
}

// run 校验配置、初始化日志后进入无限重连循环。
// 任何配置错误都以 error 返回，由 main 用非 0 退出码结束（M-19：
// 此前以退出码 0 结束会让 systemd/Docker 的 Restart= 误判为运行正常）。
func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}
	ep, err := cfg.validate()
	if err != nil {
		return err
	}

	proto.SetToken(cfg.token)
	proto.NetEncrypt = cfg.netEncrypt

	logger := log.NewLog()
	// Init 失败必须中止，否则 logerAll 为 nil，后续任一日志调用都会 panic（CRASH-02）。
	if err := logger.Init("./log", fmt.Sprintf("zc_%d_", cfg.exportPort)); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger.SetLogLevel(int32(cfg.logLevel))
	log.SetMainLog(logger)

	if cfg.token == "" {
		log.Main().Error("SECURITY: -token is empty, this client accepts any server without authentication")
	}

	log.Main().Info("try connect server %v, export port %d, forward to %v",
		net.JoinHostPort(ep.serverHost, strconv.Itoa(int(ep.serverPort))), ep.exportPort,
		net.JoinHostPort(ep.forwardHost, strconv.Itoa(int(ep.forwardPort))))

	reconnectLoop(cfg, ep)
	return nil
}

// reconnectLoop 与 cmd 的历史行为一致：断开或建隧道失败后等一段再重来，
// 永不自行退出。Stop 必须在 sleep **之前**调用——上一个实例的连接与
// inclient 没道理再多活 10 秒（LEAK-03 的一半成因）。
func reconnectLoop(cfg config, ep endpoints) {
	for {
		cli := outclient.NewClient(ep.serverHost, ep.serverPort, ep.exportPort, ep.forwardHost, ep.forwardPort)
		if err := cli.Start(); err != nil {
			log.Main().Error("start error %v", err)
		}
		_ = cli.Stop()
		log.Main().Info("wait %v for next try", reconnectDelay)
		time.Sleep(reconnectDelay)
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "ztunnel client: %v\n", err)
		os.Exit(1)
	}
}
