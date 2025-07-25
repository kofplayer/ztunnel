package inserver

import (
	"encoding/binary"
	"fmt"
	"ztunnel/common/proto"
	zServer "ztunnel/common/server"
	"ztunnel/engine/log"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	type0NetEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	type1NetEncrypt "ztunnel/engine/net/middleware/encrypt/type1"
	packageLen4Data "ztunnel/engine/net/middleware/package/len4Data"
	netMiddlewareVerifier "ztunnel/engine/net/middleware/verifier"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
	"ztunnel/server/outserver"
)

func NewServer(host string, port uint16) netServer.NetServer {
	var middlewares []netMiddleware.CreateMiddlewareFunc
	if proto.NetEncrypt {
		middlewares = []netMiddleware.CreateMiddlewareFunc{
			packageLen4Data.NewMiddleware,
			type1NetEncrypt.CreateServerNetEncryptFunc(),
			netMiddlewareVerifier.NewMiddleware,
		}
	} else {
		middlewares = []netMiddleware.CreateMiddlewareFunc{
			packageLen4Data.NewMiddleware,
			type0NetEncrypt.CreateServerNetEncryptFunc(),
		}
	}
	svr := zServer.NewServer("", port, &handler{}, netCodec.NewCodec_type8_data(), middlewares)
	return svr
}

type handler struct {
}

func (h *handler) OnConnect(s netSession.NetSession) {
}

func (h *handler) OnReady(s netSession.NetSession) {
}

func (h *handler) OnDisconnect(s netSession.NetSession) {
	bindObject := s.GetBindObject()
	if bindObject == nil {
		return
	}
	outServer := bindObject.(netServer.NetServer)
	outServer.Stop()
	log.Main().Info("client %v disconnect, stop listen", s.GetConn().RemoteAddr())
}

func (h *handler) OnMessage(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error {
	switch msgID {
	case proto.MsgIdCreateTunnel:
		if len(data) != proto.TokenLen+2 {
			log.Main().Warn("client %v data error1", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error1")
		}
		if s.GetBindObject() != nil {
			log.Main().Warn("client %v data error2", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error2")
		}
		token := string(data[:proto.TokenLen])
		if token != proto.Token {
			log.Main().Warn("client %v token error", s.GetConn().RemoteAddr())
			return fmt.Errorf("token error")
		}
		outPort := binary.BigEndian.Uint16(data[proto.TokenLen:])
		if svr := outserver.NewServer("", outPort, s); svr == nil {
			return fmt.Errorf("create tunnel fail")
		} else {
			s.SetBindObject(svr)

			log.Main().Info("client %v listen on %v", s.GetConn().RemoteAddr(), outPort)
			go func() {
				if err := svr.Start(); err != nil {
					svr.Stop()
				}
			}()
			s.SendMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeNone})
		}
	case proto.MsgIdConnectNew:
		if len(data) != netSession.SessionIDSize+1 {
			log.Main().Warn("client %v data error3", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error3")
		}
		code := data[0]
		sessionId := proto.ReadSessionId(data[1:])
		if code != proto.ErrorCodeNone {
			outServer := s.GetBindObject().(netServer.NetServer)
			session := outServer.GetSessionMgr().GetSession(sessionId)
			if session != nil {
				session.Close()
			}
		}
	case proto.MsgIdConnectDelete:
		if len(data) != netSession.SessionIDSize {
			log.Main().Warn("client %v data error4", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error4")
		}
		sessionId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		outServer := s.GetBindObject().(netServer.NetServer)
		session := outServer.GetSessionMgr().GetSession(sessionId)
		if session != nil {
			session.Close()
		}
	case proto.MsgIdConnectData:
		if len(data) < netSession.SessionIDSize {
			log.Main().Warn("client %v data error5", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error5")
		}
		bindObject := s.GetBindObject()
		if bindObject == nil {
			return fmt.Errorf("status error2")
		}
		outServer := bindObject.(netServer.NetServer)
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		session := outServer.GetSessionMgr().GetSession(connectId)
		if session != nil {
			session.SendMessage(0, 0, data[netSession.SessionIDSize:])
		}
	}
	return nil
}
