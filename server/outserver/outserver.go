package outserver

import (
	"ztunnel/common/proto"
	zServer "ztunnel/common/server"
	"ztunnel/engine/log"
	netCodec "ztunnel/engine/net/codec"
	netMiddleware "ztunnel/engine/net/middleware"
	netEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	fullData "ztunnel/engine/net/middleware/package/fullData"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
)

// NewServer 构造一条隧道的公网暴露监听。host 为绑定地址（空=通配），
// 必须真正透传——此前被硬编码为 ""，暴露端口无法限制到指定网卡（报告 SEC-02）。
func NewServer(host string, port uint16, inServerSession netSession.NetSession) netServer.NetServer {
	return zServer.NewServer(host, port, &handler{inServerSession: inServerSession}, netCodec.NewCodec_data(),
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
	// 控制通道写失败 = 隧道已死。必须关掉这条用户会话，否则终端用户会挂在
	// 一个永远等不到回应的连接上（此前返回值被完全忽略，报告 M-18）。
	if err := h.inServerSession.SendMessage(0, proto.MsgIdConnectNew, data[:]); err != nil {
		if l := log.Main(); l != nil {
			l.Error("notify ConnectNew for session %v fail: %v", s.GetID(), err)
		}
		_ = s.Close()
	}
}

func (h *handler) OnDisconnect(s netSession.NetSession) {
	data := [netSession.SessionIDSize]byte{}
	proto.WriteSessionId(data[:], s.GetID())
	// 本回调没有返回值可用，失败只能记日志，但必须可见（报告 M-18）。
	if err := h.inServerSession.SendMessage(0, proto.MsgIdConnectDelete, data[:]); err != nil {
		if l := log.Main(); l != nil {
			l.Error("notify ConnectDelete for session %v fail: %v", s.GetID(), err)
		}
	}
}

func (h *handler) OnMessage(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error {
	warpData := make([]byte, netSession.SessionIDSize, len(data)+netSession.SessionIDSize)
	proto.WriteSessionId(warpData[:], s.GetID())
	warpData = append(warpData, data...)
	// 返回 error 让引擎关闭这条用户连接：数据送不到控制通道再继续收，
	// 只会把用户数据静默倒进黑洞（报告 M-18）。
	if err := h.inServerSession.SendMessage(0, proto.MsgIdConnectData, warpData[:]); err != nil {
		if l := log.Main(); l != nil {
			l.Error("forward %v bytes for session %v fail: %v", len(data), s.GetID(), err)
		}
		return err
	}
	return nil
}
