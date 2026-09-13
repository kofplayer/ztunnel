package outclient

import (
	"encoding/binary"
	"fmt"
	"sync"
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

// Stop 停止客户端。
//
// 修复 LEAK-03：此前只向 c.c 发信号 + 关 inclient，**从不 Disconnect 控制连接**。
// 于是"建隧道 10s 超时"这一最常见路径会留下一条 ESTABLISHED 的控制连接：
// 它的 receiver/sender goroutine 常驻，服务端侧会话也认为对端仍活着并继续持有
// export 端口；外层重连循环随后新建 client → 该端口 EADDRINUSE → 再泄一条。
// **每轮重连泄一条，永不自愈，最终两端 fd 耗尽。**
//
// 通道发送改为非阻塞：Stop 可被超时回调、失败应答、重连循环多处调用，
// 裸发送依赖 chan cap=2 的隐式契约，通道满时会阻塞调用方。
func (c *outClient) Stop() error {
	select {
	case c.c <- true:
	default:
	}
	if c.cli != nil {
		_ = c.cli.Disconnect()
	}
	c.inClientMgr.CloseAllClient()
	return nil
}

const createTunnelTimeout = 10 * time.Second

type handler struct {
	outCli  *outClient
	timerMu sync.Mutex
	timer   *time.Timer
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
	// time.AfterFunc 到期前不占用 goroutine，Stop 后回调不执行。
	// 替换原先"常驻 goroutine 阻塞在 <-timer.C"的写法——
	// 那种写法在每次建隧道成功后泄漏一个永不退出的 goroutine（报告 #7）。
	h.timerMu.Lock()
	h.timer = time.AfterFunc(createTunnelTimeout, func() {
		log.Main().Error("try create tunnel timeout")
		_ = h.outCli.Stop()
	})
	h.timerMu.Unlock()
}

func (h *handler) stopCreateTunnelTimer() {
	h.timerMu.Lock()
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	h.timerMu.Unlock()
}

func (h *handler) OnDisconnect() {
	log.Main().Error("server disconnect")
	// 非阻塞：本回调跑在控制连接的 receiver goroutine 上，通道满时裸发送会
	// 阻塞读循环（cap=2 只是隐式契约）。此时 Start 早已返回，信号本就无人消费。
	select {
	case h.outCli.c <- false:
	default:
	}
}

func (h *handler) OnMessage(cb uint32, msgID uint32, data []byte) error {
	switch msgID {
	case proto.MsgIdCreateTunnel:
		h.stopCreateTunnelTimer()
		// 解析前校验长度：畸形应答此前会 data[0] 越界 panic（报告 #14）
		if len(data) < 1 {
			log.Main().Error("create tunnel response data error")
			h.outCli.Stop()
			return fmt.Errorf("create tunnel response data error")
		}
		if data[0] != proto.ErrorCodeNone {
			log.Main().Error("try create tunnel fail")
			h.outCli.Stop()
			return fmt.Errorf("create tunnel fail")
		}
		log.Main().Info("try create tunnel success")
		log.Main().Info("start success")
	case proto.MsgIdConnectNew:
		if len(data) < netSession.SessionIDSize {
			return fmt.Errorf("connect new data too short")
		}
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		var code byte = proto.ErrorCodeNone
		if _, err := h.outCli.inClientMgr.OpenClient(connectId, h.outCli.forwardHost, h.outCli.forwardPort, h.outCli.cli); err != nil {
			code = proto.ErrorCodeNormal
		}
		// 只回显 4 字节 connectId。此前是 `append([]byte{code}, data...)`
		// 把收到的**整个** payload 原样回显，而服务端要求该帧长度恒为
		// SessionIDSize+1 —— 对端只要把 ConnectNew 写长一点（版本演进、实现
		// 差异、明文通道上的注入），客户端就会亲手把长度放大回去，让自己的
		// 隧道被判死并断开（报告 M-01）。
		_data := make([]byte, 0, netSession.SessionIDSize+1)
		_data = append(_data, code)
		_data = append(_data, data[:netSession.SessionIDSize]...)
		if err := h.outCli.cli.SendMessage(0, proto.MsgIdConnectNew, _data); err != nil {
			// 控制通道写失败 = 隧道已死，必须可见（报告 M-18）
			log.Main().Error("reply ConnectNew %v fail: %v", connectId, err)
			return err
		}
	case proto.MsgIdConnectData:
		if len(data) < netSession.SessionIDSize {
			return fmt.Errorf("connect data too short")
		}
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		cli := h.outCli.inClientMgr.GetClient(connectId)
		if cli == nil {
			// 此前静默丢弃：未知 connectId 的用户数据就这样消失，既无日志也无法排障
			log.Main().Warn("no client for connectId %v, drop %v bytes", connectId, len(data)-netSession.SessionIDSize)
			return nil
		}
		if err := cli.SendMessage(0, 0, data[netSession.SessionIDSize:]); err != nil {
			// 目标是**该用户的 inclient**，失败只代表到内网服务那条链路断了。
			// 绝不能 return err —— 客户端 OnMessage 返回 error 会沿接收链上抛并
			// 关闭**控制通道**，等于用一个用户的失败拆掉整条隧道。
			// 只回收这一条 inclient，让服务端去关对应的终端用户会话。
			log.Main().Error("forward to connectId %v fail: %v, closing it", connectId, err)
			h.outCli.inClientMgr.CloseClient(connectId)
			return nil
		}
	case proto.MsgIdConnectDelete:
		if len(data) < netSession.SessionIDSize {
			return fmt.Errorf("connect delete data too short")
		}
		connectId := proto.ReadSessionId(data[:netSession.SessionIDSize])
		h.outCli.inClientMgr.CloseClient(connectId)
	}
	return nil
}
