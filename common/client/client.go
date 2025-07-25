package client

import (
	"ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	netMiddleware "ztunnel/engine/net/middleware"
)

type IClientHandler interface {
	OnConnect()
	OnReady()
	OnDisconnect()
	OnMessage(cb uint32, msgID uint32, data []byte) error
}

func NewClient(host string, port uint16, handler IClientHandler, codec netCodec.Codec, middlewares []netMiddleware.CreateMiddlewareFunc) client.NetClient {
	conn := socketNetConnect.NewConnector()
	conn.SetAddress(host, port)
	cli := client.NewNetClient()
	cli.SetConnector(conn)
	cli.SetCodec(codec)
	cli.SetOnConnect(handler.OnConnect)
	cli.SetOnReady(handler.OnReady)
	cli.SetOnDisconnect(handler.OnDisconnect)
	cli.SetOnMessage(handler.OnMessage)
	for _, f := range middlewares {
		cli.AddMiddleware(f)
	}
	return cli
}
