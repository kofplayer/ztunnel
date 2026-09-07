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
	// Stop 现在会同时关闭该 outserver 的全部存量用户会话（报告 #9）
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
		if outPort == 0 {
			log.Main().Warn("client %v invalid out port 0", s.GetConn().RemoteAddr())
			s.SendMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeNormal})
			return nil
		}
		svr := outserver.NewServer("", outPort, s)
		// 同步绑定端口：失败立即回错误码，避免异步 Start 失败被吞后的"假成功"（报告 #8）
		if err := svr.Listen(); err != nil {
			log.Main().Warn("client %v create tunnel on port %v fail: %v", s.GetConn().RemoteAddr(), outPort, err)
			s.SendMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeNormal})
			return nil
		}
		s.SetBindObject(svr)

		log.Main().Info("client %v listen on %v", s.GetConn().RemoteAddr(), outPort)
		go func() {
			if err := svr.Start(); err != nil {
				svr.Stop()
			}
		}()
		s.SendMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeNone})
	case proto.MsgIdConnectNew:
		if len(data) != netSession.SessionIDSize+1 {
			log.Main().Warn("client %v data error3", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error3")
		}
		code := data[0]
		sessionId := proto.ReadSessionId(data[1:])
		if code != proto.ErrorCodeNone {
			outServer, err := h.bindOutServer(s, "3")
			if err != nil {
				return err
			}
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
		outServer, err := h.bindOutServer(s, "4")
		if err != nil {
			return err
		}
		session := outServer.GetSessionMgr().GetSession(sessionId)
		if session != nil {
			session.Close()
		}
	case proto.MsgIdConnectData:
		if len(data) < netSession.SessionIDSize {
			log.Main().Warn("client %v data error5", s.GetConn().RemoteAddr())
			return fmt.Errorf("data error5")
		}
		outServer, err := h.bindOutServer(s, "5")
		if err != nil {
			return err
		}
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		session := outServer.GetSessionMgr().GetSession(connectId)
		if session != nil {
			session.SendMessage(0, 0, data[netSession.SessionIDSize:])
		}
	}
	return nil
}

// bindOutServer 取会话绑定的 outserver。未建隧道（BindObject 为 nil）时
// 必须拒绝消息而非断言 nil——此前任意未认证连接发一条 ConnectDelete 即可
// 触发 nil 接口断言 panic 崩溃整个进程（报告 #2）。
func (h *handler) bindOutServer(s netSession.NetSession, errNo string) (netServer.NetServer, error) {
	bindObject := s.GetBindObject()
	if bindObject == nil {
		log.Main().Warn("client %v status error%v", s.GetConn().RemoteAddr(), errNo)
		return nil, fmt.Errorf("status error%v", errNo)
	}
	return bindObject.(netServer.NetServer), nil
}
