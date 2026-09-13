package socketNetConnect

import (
	"net"
	"strconv"
	"time"
	netConnect "ztunnel/engine/net/connect"
)

func NewConnector() *ConnectorSocket {
	v := &ConnectorSocket{
		ConnSocket: newConn(nil),
	}
	return v
}

type ConnectorSocket struct {
	onConnectFunc netConnect.OnConnectFunc
	*ConnSocket
	host string
	port uint16
}

func (this *ConnectorSocket) Connect() error {
	conn, err := net.Dial("tcp", net.JoinHostPort(this.host, strconv.Itoa(int(this.port))))
	if err != nil {
		return err
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	this.ConnSocket.conn = conn
	go this.receiverRun()
	go this.senderRun()
	// 判空：与 ConnSocket.recoverPanic 的处理保持一致，未设置回调时不得 nil 调用
	if this.onConnectFunc != nil {
		this.onConnectFunc()
	}
	return nil
}

func (this *ConnectorSocket) SetOnConnect(onConnectFunc netConnect.OnConnectFunc) {
	this.onConnectFunc = onConnectFunc
}

func (this *ConnectorSocket) SetAddress(host string, port uint16) {
	this.host = host
	this.port = port
}
