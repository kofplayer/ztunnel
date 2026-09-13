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

// DialTimeout 是 TCP 拨号的最长等待时间。
//
// 修复 DOS-01 的一部分：原先是裸 `net.Dial`，**没有任何超时**。目标被防火墙
// DROP（而非 REJECT）时会一直等到内核默认超时（Linux/Windows 约 21 秒），
// 期间调用方 goroutine 完全卡住。设为包级变量是为了让入口（-dial_timeout）
// 能在启动时一次性覆盖；运行期不要改它。
var DialTimeout = 5 * time.Second

func (this *ConnectorSocket) Connect() error {
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.Dial("tcp", net.JoinHostPort(this.host, strconv.Itoa(int(this.port))))
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
