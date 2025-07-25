package inclient

import (
	zClient "ztunnel/common/client"
	"ztunnel/common/proto"
	"ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	netEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	fullData "ztunnel/engine/net/middleware/package/fullData"
	netSession "ztunnel/engine/net/session"
)

func NewClient(connectId netSession.SessionID, host string, port uint16, outcli client.NetClient) client.NetClient {
	cli := zClient.NewClient(host, port, &handler{outcli: outcli, connectId: connectId}, netCodec.NewCodec_data(),
		[]netMiddleware.CreateMiddlewareFunc{
			fullData.NewMiddleware,
			netEncrypt.CreateServerNetEncryptFunc(),
		})
	return cli
}

type handler struct {
	outcli    client.NetClient
	connectId netSession.SessionID
}

func (h *handler) OnConnect() {
}

func (h *handler) OnReady() {
}

func (h *handler) OnDisconnect() {
	data := [netSession.SessionIDSize]byte{}
	proto.WriteSessionId(data[:], h.connectId)
	h.outcli.SendMessage(0, proto.MsgIdConnectDelete, data[:])
}

func (h *handler) OnMessage(cb uint32, msgID uint32, data []byte) error {
	warpData := make([]byte, netSession.SessionIDSize, len(data)+netSession.SessionIDSize)
	proto.WriteSessionId(warpData[:], h.connectId)
	warpData = append(warpData, data...)
	h.outcli.SendMessage(0, proto.MsgIdConnectData, warpData[:])
	return nil
}
