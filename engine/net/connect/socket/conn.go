package socketNetConnect

import (
	"net"
	"sync"

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
	// disconnectOnce 保证断开通知在**所有**关闭路径上恰好执行一次。
	//
	// 这里原先用的是 `if !q.IsClose()`。该谓词描述的是队列状态，与"业务是否
	// 已收到断开通知"无关，而主动关闭恰好会先把队列关掉——于是
	// session.Close() 一类的本地关闭会整体跳过 OnDisconnect 链，
	// 使唯一的会话清理点（netServer 的 RemoveSession）永不执行（报告 SEC-01）。
	disconnectOnce sync.Once
}

func (this *ConnSocket) RemoteAddr() string {
	conn := this.conn
	if conn == nil {
		return ""
	}
	addr := conn.RemoteAddr()
	if addr == nil {
		return ""
	}
	return addr.String()
}

// Disconnect 关闭发送队列（senderRun 排空后负责关闭 TCP），并通知业务断开。
//
// 通知必须发生在这里：主动 Disconnect 的调用方（session.Close /
// netClient.Disconnect）不会再经过 receiverRun 的错误分支。
func (this *ConnSocket) Disconnect() error {
	err := this.q.Close()
	this.fireDisconnect()
	return err
}

// Abort 强制关闭：清空发送队列并立即关闭底层 TCP 连接。
// 用于 recover 兜底路径（onAccept 回调 panic 时回调尚未启动收发 goroutine，
// 仅关队列无法释放 TCP 连接）。
func (this *ConnSocket) Abort() {
	_ = this.q.Close()
	if this.conn != nil {
		_ = this.conn.Close()
	}
	this.fireDisconnect()
}

// fireDisconnect 幂等地派发断开通知。
//
// 回调未安装时**不得**消耗 sync.Once：accept 路径上 onDisconnectFunc 是在
// safeAccept 内安装的，若此前就 panic 并触发本函数，一次空的 Do 会把
// 唯一的通知机会烧掉，令会话永久残留在 map 里。
func (this *ConnSocket) fireDisconnect() {
	f := this.onDisconnectFunc
	if f == nil {
		return
	}
	this.disconnectOnce.Do(f)
}

func (this *ConnSocket) SendData(data []byte) error {
	return this.q.Enqueue(data)
}

func (this *ConnSocket) SetOnDisconnect(onDisconnectFunc netConnect.OnDisconnectFunc) {
	this.onDisconnectFunc = onDisconnectFunc
}

func (this *ConnSocket) SetOnData(onDataFunc netConnect.OnDataFunc) {
	this.onDataFunc = onDataFunc
}

func (this *ConnSocket) receiverRun() {
	defer this.recoverPanic("receiver")
	// 不使用 bufio：本函数只调用 Read 且每次都用完整的 4096 字节缓冲，
	// bufio.Reader 在这种情况下本就是直读底层 conn（不多一次拷贝），
	// 留着它只是每连接白分配 4096 字节（报告 P-01）。
	var buf [4096]byte
	for {
		n, err := this.conn.Read(buf[:])
		if err != nil {
			this.Disconnect()
			return
		}
		err = this.onDataFunc(buf[:n])
		if err != nil {
			this.Disconnect()
			return
		}
	}
}

func (this *ConnSocket) senderRun() {
	defer this.recoverPanic("sender")
	for {
		data, ok := this.q.Dequeue()
		if !ok {
			_ = this.conn.Close()
			return
		}
		msg, ok := data.([]byte)
		if !ok {
			// 类型断言失败说明发送队列里混入了非 []byte，属于上层接线 bug。
			// 按连接异常关闭处理，不能让 senderRun 静默退出（报告 LEAK-02）。
			logSenderFault("sender", this.RemoteAddr(), "queued item is not []byte")
			this.Abort()
			return
		}
		for len(msg) > 0 {
			n, err := this.conn.Write(msg)
			if err != nil {
				// 写失败必须释放 fd 并通知业务。此前这里只有一个裸 return：
				// senderRun 退出而队列仍未关闭，于是 ① TCP fd 直到进程退出都不释放
				// （receiver 后续读错误只会 q.Close()，全仓库再无别处关 conn）；
				// ② SendData 继续"恒成功"，业务以为已发出，数据堆在无人消费的队列里；
				// ③ 若对端只是半关闭，receiver 永久阻塞在 Read → 2 个 goroutine 全泄漏
				// （报告 LEAK-02）。
				this.Abort()
				return
			}
			msg = msg[n:]
		}
	}
}

// recoverPanic 兜底收发循环内的 panic（中间件链/handler 的任何上层 bug），
// 恢复后按连接断开处理，保证单个连接的异常不会杀死整个进程。
// 注意：本函数自身绝不能再 panic——日志后端可能未初始化。
func (this *ConnSocket) recoverPanic(role string) {
	if r := recover(); r != nil {
		logSenderFault(role, this.RemoteAddr(), r)
		this.Abort()
	}
}

// logSenderFault 在兜底路径上记日志，且**自身绝不 panic**：
// log.Main() 可能为 nil（上层未初始化日志），而 nil 接收者上调方法会再次 panic，
// 把"兜底的兜底"变成杀死进程的那一下（报告 CRASH-02 的可达形态之一）。
func logSenderFault(role string, addr string, v interface{}) {
	l := log.Main()
	if l == nil {
		return
	}
	l.Error("conn %v %s fault: %v", addr, role, v)
}
