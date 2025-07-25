package outclient

import (
	"encoding/binary"
	"fmt"
	"time"
	"ztunnel/client/inclient"
	zClient "ztunnel/common/client"
	"ztunnel/common/proto"
	"ztunnel/engine/log"
	"ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	type0NetEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	type1NetEncrypt "ztunnel/engine/net/middleware/encrypt/type1"
	packageLen4Data "ztunnel/engine/net/middleware/package/len4Data"
	netMiddlewareVerifier "ztunnel/engine/net/middleware/verifier"
	netSession "ztunnel/engine/net/session"
)

type Client interface {
	Start() error
	Stop() error
}

func NewClient(host string, port uint16, svrListenPort uint16, forwardHost string, forwardPort uint16) Client {
	c := &outClient{
		svrListenPort: svrListenPort,
		forwardHost:   forwardHost,
		forwardPort:   forwardPort,
		inClientMgr:   inclient.NewClientMgr(),
		c:             make(chan bool, 2),
	}
	h := &handler{
		outCli: c,
	}

	var middlewares []netMiddleware.CreateMiddlewareFunc
	if proto.NetEncrypt {
		middlewares = []netMiddleware.CreateMiddlewareFunc{
			packageLen4Data.NewMiddleware,
			type1NetEncrypt.NewClientNetEncrypt,
			netMiddlewareVerifier.NewMiddleware,
		}
	} else {
		middlewares = []netMiddleware.CreateMiddlewareFunc{
			packageLen4Data.NewMiddleware,
			type0NetEncrypt.NewClientNetEncrypt,
		}
	}

	c.cli = zClient.NewClient(host, port, h, netCodec.NewCodec_type8_data(), middlewares)
	return c
}

type outClient struct {
	cli           client.NetClient
	svrListenPort uint16
	forwardHost   string
	forwardPort   uint16
	inClientMgr   *inclient.ClientMgr
	c             chan bool
}

func (c *outClient) Start() error {
	if err := c.cli.Connect(); err != nil {
		return err
	}
	<-c.c
	return nil
}

func (c *outClient) Stop() error {
	c.c <- true
	c.inClientMgr.CloseAllClient()
	return nil
}

type handler struct {
	outCli *outClient
	timer  *time.Timer
}

func (h *handler) OnConnect() {
}

func (h *handler) OnReady() {
	data := make([]byte, 0, proto.TokenLen+2)
	data = append(data, proto.Token...)
	data = binary.BigEndian.AppendUint16(data, h.outCli.svrListenPort)
	h.outCli.cli.SendMessage(0, proto.MsgIdCreateTunnel, data[:])
	log.Main().Info("connect server ok")
	log.Main().Info("try create tunnel server:%v -> %v:%v", h.outCli.svrListenPort, h.outCli.forwardHost, h.outCli.forwardPort)
	h.timer = time.NewTimer(time.Second * 10)
	go func() {
		_, ok := <-h.timer.C
		if ok {
			log.Main().Error("try create tunnel timeout")
			h.outCli.Stop()
		}
	}()
}

func (h *handler) OnDisconnect() {
	log.Main().Error("server disconnect")
	h.outCli.c <- false
}

func (h *handler) OnMessage(cb uint32, msgID uint32, data []byte) error {
	switch msgID {
	case proto.MsgIdCreateTunnel:
		h.timer.Stop()
		h.timer = nil
		if data[0] != proto.ErrorCodeNone {
			log.Main().Error("try create tunnel fail")
			h.outCli.Stop()
			return fmt.Errorf("create tunnel fail")
		} else {
			log.Main().Error("try create tunnel success")
			log.Main().Info("start success")
		}
	case proto.MsgIdConnectNew:
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		var code byte = proto.ErrorCodeNone
		if _, err := h.outCli.inClientMgr.OpenClient(connectId, h.outCli.forwardHost, h.outCli.forwardPort, h.outCli.cli); err != nil {
			code = proto.ErrorCodeNormal
		}
		_data := append([]byte{code}, data...)
		h.outCli.cli.SendMessage(0, proto.MsgIdConnectNew, _data)
	case proto.MsgIdConnectData:
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		cli := h.outCli.inClientMgr.GetClient(connectId)
		if cli != nil {
			cli.SendMessage(0, 0, data[netSession.SessionIDSize:])
		}
	case proto.MsgIdConnectDelete:
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		h.outCli.inClientMgr.CloseClient(connectId)
	}
	return nil
}
