package main

import (
	"flag"
	"fmt"
	"time"
	"ztunnel/client/outclient"
	"ztunnel/common/proto"
	"ztunnel/common/util"
	"ztunnel/engine/log"
)

var (
	log_level   = flag.Int("log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	server      = flag.String("server", "localhost:8888", "server address")
	forward     = flag.String("forward", "localhost:9999", "forward address")
	export_port = flag.Int("export_port", 9999, "server export port")
	net_encrypt = flag.Bool("net_encrypt", false, "encrypt data between client and server (default false)")
	token       = flag.String("token", "", "client connect to server token")
)

func main() {
	flag.Parse()
	proto.SetToken(*token)
	proto.NetEncrypt = *net_encrypt
	logger := log.NewLog()
	logger.Init("./log", fmt.Sprintf("zc_%v_", *export_port))
	logger.SetLogLevel(int32(*log_level))
	log.SetMainLog(logger)

	serverHost, serverPort, err := util.GetHostAndPort(*server)
	if err != nil {
		log.Main().Error("server err:%v", err)
		return
	}

	forwardHost, forwardPort, err := util.GetHostAndPort(*forward)
	if err != nil {
		log.Main().Error("forward err:%v", err)
		return
	}

	for {
		cli := outclient.NewClient(serverHost, serverPort, uint16(*export_port), forwardHost, forwardPort)
		log.Main().Info("try connect server %v:%v", serverHost, serverPort)
		err := cli.Start()
		if err != nil {
			log.Main().Error("start error %v", err)
		}
		log.Main().Info("wait 10s for next try")
		time.Sleep(time.Second * 10)
		cli.Stop()
	}
}
