package server

import (
	netCodec "ztunnel/engine/net/codec"
	netConnect "ztunnel/engine/net/connect"
	netMiddleware "ztunnel/engine/net/middleware"
	netMiddlewareCommon "ztunnel/engine/net/middleware/common"
	netSession "ztunnel/engine/net/session"
)

func NewNetServer() NetServer {
	v := new(netServer)
	v.sessionMgr = netSession.NewSessionMgr()
	return v
}

type NetServer interface {
	SetAcceptor(acceptor netConnect.Acceptor)
	SetCodec(codec netCodec.Codec)
	SetOnAccept(func(netSession.NetSession))
	SetOnReady(func(netSession.NetSession))
	SetOnDisconnect(func(netSession.NetSession))
	SetOnMessage(func(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error)
	Start() error
	Stop() error
	GetSessionMgr() netSession.SessionMgr
	AddMiddleware(f netMiddleware.CreateMiddlewareFunc)
}

type netServer struct {
	acceptor                 netConnect.Acceptor
	codec                    netCodec.Codec
	onAccept                 func(netSession.NetSession)
	onReady                  func(netSession.NetSession)
	onDisconnect             func(netSession.NetSession)
	onMessage                func(s netSession.NetSession, cb uint32, t uint32, data []byte) error
	sessionMgr               netSession.SessionMgr
	middlewareCreateFuncList []func() netMiddleware.Middleware
}

func (this *netServer) SetAcceptor(acceptor netConnect.Acceptor) {
	this.acceptor = acceptor
}

func (this *netServer) SetCodec(codec netCodec.Codec) {
	this.codec = codec
}

func (this *netServer) AddMiddleware(f netMiddleware.CreateMiddlewareFunc) {
	this.middlewareCreateFuncList = append(this.middlewareCreateFuncList, f)
}

func (this *netServer) SetOnAccept(onAccept func(netSession.NetSession)) {
	this.onAccept = onAccept
}

func (this *netServer) SetOnReady(onReady func(netSession.NetSession)) {
	this.onReady = onReady
}

func (this *netServer) SetOnDisconnect(onDisconnect func(netSession.NetSession)) {
	this.onDisconnect = onDisconnect
}

func (this *netServer) SetOnMessage(onMessage func(s netSession.NetSession, cb uint32, t uint32, data []byte) error) {
	this.onMessage = onMessage
}

func (this *netServer) Start() error {
	this.acceptor.SetOnAccept(func(conn netConnect.Conn) {
		firstMiddleware := netMiddlewareCommon.NewMiddlewareFirst(func(data []byte) error {
			return conn.SendData(data)
		})
		currentMiddleware := firstMiddleware
		for _, f := range this.middlewareCreateFuncList {
			_m := f()
			_m.SetPre(currentMiddleware)
			currentMiddleware.SetNext(_m)
			currentMiddleware = _m
		}
		s := this.sessionMgr.NewSession()
		s.SetConn(conn)
		lastMiddleware := netMiddlewareCommon.NewMiddlewareLast(func(data []byte) error {
			cb, msgID, msgData, err := this.codec.Decode(data)
			if err != nil {
				return err
			}
			return this.onMessage(s, cb, msgID, msgData)
		}, func() {
			if this.onReady != nil {
				this.onReady(s)
			}
		})
		lastMiddleware.SetPre(currentMiddleware)
		currentMiddleware.SetNext(lastMiddleware)
		s.SetSendMessageFunc(func(cb uint32, msgID uint32, data []byte) error {
			pkgData, err := this.codec.Encode(cb, msgID, data)
			if err != nil {
				return err
			}
			return lastMiddleware.SendData(pkgData)
		})
		conn.SetOnDisconnect(func() {
			firstMiddleware.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
			this.onDisconnect(s)
			this.sessionMgr.RemoveSession(s.GetID())
		})
		conn.SetOnData(func(data []byte) error {
			return firstMiddleware.ReceiveData(data)
		})
		firstMiddleware.FireEvent(netMiddleware.MiddlewareEventOnConnect)
		this.onAccept(s)
	})
	return this.acceptor.Start()
}

func (this *netServer) Stop() error {
	return this.acceptor.Stop()
}

func (this *netServer) GetSessionMgr() netSession.SessionMgr {
	return this.sessionMgr
}
