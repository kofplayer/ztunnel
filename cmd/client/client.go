package main

import (
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

var (
	log_level   = flag.Int("log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	server      = flag.String("server", "localhost:8888", "server address (host:port)")
	forward     = flag.String("forward", "localhost:9999", "forward address (host:port)")
	export_port = flag.Uint("export_port", 9999, "server export port (1-65535)")
	net_encrypt = flag.Bool("net_encrypt", false, "encrypt data between client and server (default false)")
	token       = flag.String("token", "", "client connect to server token")
)

// run 校验全部配置后进入无限重连循环。任何配置错误都以非 0 退出码结束，
// 不能以 exit 0 收场（报告 M-19/ROBUST-02）。
func run() error {
	flag.Parse()

	if *log_level < log.DEBUG || *log_level > log.NONE {
		return fmt.Errorf("invalid -log_level %d (must be %d-%d)", *log_level, log.DEBUG, log.NONE)
	}
	// 此前是 flag.Int + uint16() 强转且无范围校验：-export_port=70000 会静默
	// 变成 4464（暴露错端口），=65536 变成 0（进入重连死循环）（报告 M-20）。
	if *export_port == 0 || *export_port > 65535 {
		return fmt.Errorf("invalid -export_port %d (must be 1-65535)", *export_port)
	}
	serverHost, serverPort, err := util.GetHostAndPort(*server)
	if err != nil {
		return fmt.Errorf("invalid -server: %w", err)
	}
	forwardHost, forwardPort, err := util.GetHostAndPort(*forward)
	if err != nil {
		return fmt.Errorf("invalid -forward: %w", err)
	}

	proto.SetToken(*token)
	proto.NetEncrypt = *net_encrypt

	logger := log.NewLog()
	// Init 失败必须中止，否则 logerAll 为 nil，后续任一日志调用都会 panic（报告 CRASH-02）。
	if err := logger.Init("./log", fmt.Sprintf("zc_%d_", *export_port)); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger.SetLogLevel(int32(*log_level))
	log.SetMainLog(logger)

	if *token == "" {
		log.Main().Error("SECURITY: -token is empty, this client accepts any server without authentication")
	}

	log.Main().Info("try connect server %v, export port %d, forward to %v",
		net.JoinHostPort(serverHost, strconv.Itoa(int(serverPort))), *export_port,
		net.JoinHostPort(forwardHost, strconv.Itoa(int(forwardPort))))

	for {
		cli := outclient.NewClient(serverHost, serverPort, uint16(*export_port), forwardHost, forwardPort)
		if err := cli.Start(); err != nil {
			log.Main().Error("start error %v", err)
		}
		// Stop 必须在重连之前调用：此前顺序是 sleep→Stop→新建，
		// 上一个实例的连接与 inclient 要额外多活 10s。
		_ = cli.Stop()
		log.Main().Info("wait 10s for next try")
		time.Sleep(time.Second * 10)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ztunnel client: %v\n", err)
		os.Exit(1)
	}
}
