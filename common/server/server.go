package server

import (
	netCodec "ztunnel/engine/net/codec"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	netMiddleware "ztunnel/engine/net/middleware"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
)

type IServerHandler interface {
	OnConnect(s netSession.NetSession)
	OnReady(s netSession.NetSession)
	OnDisconnect(s netSession.NetSession)
	OnMessage(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error
}

func NewServer(host string, port uint16, handler IServerHandler, codec netCodec.Codec, middlewares []netMiddleware.CreateMiddlewareFunc) netServer.NetServer {
	svr := netServer.NewNetServer()
	acceptor := socketNetConnect.NewAcceptor()
	acceptor.SetAddress(host, port)
	svr.SetAcceptor(acceptor)
	svr.SetCodec(codec)
	for _, f := range middlewares {
		svr.AddMiddleware(f)
	}
	svr.SetOnAccept(handler.OnConnect)
	svr.SetOnReady(handler.OnReady)
	svr.SetOnDisconnect(func(s netSession.NetSession) {
		handler.OnDisconnect(s)
	})
	svr.SetOnMessage(func(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error {
		err := handler.OnMessage(s, cb, msgID, data)
		if err != nil {
			s.Close()
		}
		return err
	})
	return svr
}

func StartServer(svr netServer.NetServer) error {
	go func() {
		err := svr.Start()
		if err != nil {
			panic(err)
		}
	}()
	return nil
}
