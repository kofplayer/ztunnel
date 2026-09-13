package inclient

import (
	zClient "ztunnel/common/client"
	"ztunnel/common/proto"
	"ztunnel/engine/log"
	"ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	netEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	fullData "ztunnel/engine/net/middleware/package/fullData"
	netSession "ztunnel/engine/net/session"
)

func NewClient(connectId netSession.SessionID, host string, port uint16, outcli client.NetClient, mgr *ClientMgr) client.NetClient {
	h := &handler{mgr: mgr, outcli: outcli, connectId: connectId}
	cli := zClient.NewClient(host, port, h, netCodec.NewCodec_data(),
		[]netMiddleware.CreateMiddlewareFunc{
			fullData.NewMiddleware,
			netEncrypt.CreateServerNetEncryptFunc(),
		})
	// 回填自身：OnDisconnect 调 RemoveClient 时要用身份核对，避免同 id 已被新
	// 连接接管时误删新条目（报告 M-15 / M-03）。
	h.self = cli
	return cli
}

type handler struct {
	mgr       *ClientMgr
	outcli    client.NetClient
	connectId netSession.SessionID
	self      client.NetClient
}

func (h *handler) OnConnect() {
}

func (h *handler) OnReady() {
}

func (h *handler) OnDisconnect() {
	data := [netSession.SessionIDSize]byte{}
	proto.WriteSessionId(data[:], h.connectId)
	// 通知服务端关闭对应的终端用户会话。此刻控制通道可能已经先一步死了，
	// 发送失败只能记日志（本回调没有返回值可用），但必须**可见**（报告 M-18）。
	if err := h.outcli.SendMessage(0, proto.MsgIdConnectDelete, data[:]); err != nil {
		if l := log.Main(); l != nil {
			l.Error("notify ConnectDelete for connectId %v fail: %v", h.connectId, err)
		}
	}
	h.mgr.RemoveClient(h.connectId, h.self)
}

func (h *handler) OnMessage(cb uint32, msgID uint32, data []byte) error {
	warpData := make([]byte, netSession.SessionIDSize, len(data)+netSession.SessionIDSize)
	proto.WriteSessionId(warpData[:], h.connectId)
	warpData = append(warpData, data...)
	// 此前发送失败被完全吞掉：用户数据静默丢失，两侧都以为连接正常（报告 M-18）。
	// 返回 error 会让引擎关闭**本条**到内网服务的连接（不会拆控制通道），
	// 并由 OnDisconnect 补发 ConnectDelete 让服务端关闭终端用户会话。
	if err := h.outcli.SendMessage(0, proto.MsgIdConnectData, warpData[:]); err != nil {
		if l := log.Main(); l != nil {
			l.Error("forward %v bytes for connectId %v fail: %v", len(data), h.connectId, err)
		}
		return err
	}
	return nil
}
