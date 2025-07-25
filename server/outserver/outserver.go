package outserver

import (
	"ztunnel/common/proto"
	zServer "ztunnel/common/server"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	netEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	fullData "ztunnel/engine/net/middleware/package/fullData"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
)

func NewServer(host string, port uint16, inServerSession netSession.NetSession) netServer.NetServer {
	return zServer.NewServer("", port, &handler{inServerSession: inServerSession}, netCodec.NewCodec_data(),
		[]netMiddleware.CreateMiddlewareFunc{
			fullData.NewMiddleware,
			netEncrypt.CreateServerNetEncryptFunc(),
		})
}

type handler struct {
	inServerSession netSession.NetSession
}

func (h *handler) OnConnect(s netSession.NetSession) {
}

func (h *handler) OnReady(s netSession.NetSession) {
	data := [netSession.SessionIDSize]byte{}
	proto.WriteSessionId(data[:], s.GetID())
	h.inServerSession.SendMessage(0, proto.MsgIdConnectNew, data[:])
}

func (h *handler) OnDisconnect(s netSession.NetSession) {
	data := [netSession.SessionIDSize]byte{}
	proto.WriteSessionId(data[:], s.GetID())
	h.inServerSession.SendMessage(0, proto.MsgIdConnectDelete, data[:])
}

func (h *handler) OnMessage(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error {
	warpData := make([]byte, netSession.SessionIDSize, len(data)+netSession.SessionIDSize)
	proto.WriteSessionId(warpData[:], s.GetID())
	warpData = append(warpData, data...)
	h.inServerSession.SendMessage(0, proto.MsgIdConnectData, warpData[:])
	return nil
}
