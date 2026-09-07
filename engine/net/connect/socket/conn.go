package socketNetConnect

import (
	"bufio"
	"net"

	"ztunnel/engine/log"
	netConnect "ztunnel/engine/net/connect"

	"ztunnel/engine/queue"
	queueDef "ztunnel/engine/queue/def"
)

func newConn(conn net.Conn) *ConnSocket {
	v := new(ConnSocket)
	v.q = queue.NewQueue(32)
	v.conn = conn
	return v
}

type ConnSocket struct {
	q                queueDef.Queue
	onDisconnectFunc netConnect.OnDisconnectFunc
	onDataFunc       netConnect.OnDataFunc
	conn             net.Conn
}

func (this *ConnSocket) RemoteAddr() string {
	if this.conn == nil || this.conn.RemoteAddr() == nil {
		return ""
	}
	return this.conn.RemoteAddr().String()
}

func (this *ConnSocket) Disconnect() error {
	return this.q.Close()
}

// Abort 强制关闭：清空发送队列并立即关闭底层 TCP 连接。
// 用于 recover 兜底路径（onAccept 回调 panic 时回调尚未启动收发 goroutine，
// 仅关队列无法释放 TCP 连接）。
func (this *ConnSocket) Abort() {
	_ = this.q.Close()
	if this.conn != nil {
		_ = this.conn.Close()
	}
}

func (this *ConnSocket) SendData(data []byte) error {
	err := this.q.Enqueue(data)
	if err != nil {
		return err
	}
	return nil
}

func (this *ConnSocket) SetOnDisconnect(onDisconnectFunc netConnect.OnDisconnectFunc) {
	this.onDisconnectFunc = onDisconnectFunc
}

func (this *ConnSocket) SetOnData(onDataFunc netConnect.OnDataFunc) {
	this.onDataFunc = onDataFunc
}

func (this *ConnSocket) receiverRun() {
	defer this.recoverPanic("receiver")
	reader := bufio.NewReader(this.conn)
	var buf [4096]byte
	for {
		n, err := reader.Read(buf[:])
		if err != nil {
			if !this.q.IsClose() {
				this.Disconnect()
				this.onDisconnectFunc()
			}
			return
		}
		err = this.onDataFunc(buf[:n])
		if err != nil {
			if !this.q.IsClose() {
				this.Disconnect()
				this.onDisconnectFunc()
			}
			return
		}
	}
}

func (this *ConnSocket) senderRun() {
	defer this.recoverPanic("sender")
	for {
		data, ok := this.q.Dequeue()
		if !ok {
			this.conn.Close()
			return
		}
		msg := data.([]byte)
		for len(msg) > 0 {
			n, err := this.conn.Write(msg)
			if err != nil {
				return
			}
			msg = msg[n:]
		}
	}
}

// recoverPanic 兜底收发循环内的 panic（中间件链/handler 的任何上层 bug），
// 恢复后按连接断开处理，保证单个连接的异常不会杀死整个进程。
// 注意：本函数自身绝不能再 panic——log.Main() 可能为 nil（上层未初始化日志）。
func (this *ConnSocket) recoverPanic(role string) {
	if r := recover(); r != nil {
		if l := log.Main(); l != nil {
			l.Error("conn %v %s panic: %v", this.RemoteAddr(), role, r)
		}
		if !this.q.IsClose() {
			this.Abort()
			if this.onDisconnectFunc != nil {
				this.onDisconnectFunc()
			}
		}
	}
}
