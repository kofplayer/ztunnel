package main

import (
	"flag"
	"ztunnel/common/proto"
	"ztunnel/common/util"
	"ztunnel/engine/log"
	"ztunnel/server/inserver"
)

var (
	log_level   = flag.Int("log_level", log.DEBUG, "log level DEBUG:0 INFO:1 WARN:2 ERROR:3 FATAL:4 NONE:5 (default 0)")
	listen      = flag.String("listen", ":8888", "server listen address")
	token       = flag.String("token", "", "client connect to server token")
	net_encrypt = flag.Bool("net_encrypt", false, "encrypt data between client and server (default false)")
)

func main() {
	flag.Parse()
	proto.SetToken(*token)
	proto.NetEncrypt = *net_encrypt
	logger := log.NewLog()
	logger.Init("./log", "zs_")
	logger.SetLogLevel(int32(*log_level))
	log.SetMainLog(logger)
	host, port, err := util.GetHostAndPort(*listen)
	if err != nil {
		log.Main().Error("listen err:%v", err)
		return
	}
	log.Main().Info("start server on %v:%v", host, port)
	err = inserver.NewServer(host, port).Start()
	if err != nil {
		log.Main().Error("%v", err)
	}
}
