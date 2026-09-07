package socketNetConnect

import (
	"net"
	"strconv"
	"time"

	"ztunnel/engine/log"
	netConnect "ztunnel/engine/net/connect"
)

func NewAcceptor() *AcceptorSocket {
	v := new(AcceptorSocket)
	return v
}

type AcceptorSocket struct {
	onAcceptFunc netConnect.OnAcceptFunc
	host         string
	port         uint16
	listener     net.Listener
}

// Listen 预绑定端口。绑定失败立即暴露给调用方，
// 避免异步 Start 失败被吞后向客户端返回"假成功"（报告 #8）。
func (this *AcceptorSocket) Listen() error {
	if this.listener != nil {
		return nil
	}
	l, err := net.Listen("tcp", this.host+":"+strconv.Itoa(int(this.port)))
	if err != nil {
		return err
	}
	this.listener = l
	return nil
}

func (this *AcceptorSocket) Start() error {
	if this.listener == nil {
		if err := this.Listen(); err != nil {
			return err
		}
	}
	for {
		conn, err := this.listener.Accept()
		if err != nil {
			return err
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}
		c := newConn(conn)
		this.safeAccept(c)
		go c.receiverRun()
		go c.senderRun()
	}
}

func (this *AcceptorSocket) Stop() error {
	if this.listener != nil {
		this.listener.Close()
	}
	return nil
}

func (this *AcceptorSocket) SetOnAccept(onAcceptFunc netConnect.OnAcceptFunc) {
	this.onAcceptFunc = onAcceptFunc
}

func (this *AcceptorSocket) SetAddress(host string, port uint16) {
	this.host = host
	this.port = port
}

// safeAccept 兜底 onAccept 回调中的 panic：任何上层 bug 都不允许
// 杀死 accept 循环乃至整个进程（项目无全局 recover，报告 #1/#2 的放大器）。
// log.Main() 可能未初始化（nil），不能让兜底路径自身 panic。
func (this *AcceptorSocket) safeAccept(c *ConnSocket) {
	defer func() {
		if r := recover(); r != nil {
			if l := log.Main(); l != nil {
				l.Error("accept %v panic: %v", c.RemoteAddr(), r)
			}
			c.Abort()
		}
	}()
	this.onAcceptFunc(c)
}
