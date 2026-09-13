package socketNetConnect

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"ztunnel/engine/log"
	netConnect "ztunnel/engine/net/connect"
)

func NewAcceptor() *AcceptorSocket {
	v := new(AcceptorSocket)
	return v
}

type AcceptorSocket struct {
	// mu 保护 listener / stopped / onAcceptFunc / host / port。
	// 这三个字段会被三方的不同 goroutine 触碰：Listen() 由建隧道的 goroutine 调
	// （inserver 的 receiver），Start() 跑在独立 goroutine 的 accept 循环里，
	// Stop() 又由控制通道的 OnDisconnect 触发（报告 M-13 / L-7）。
	mu           sync.Mutex
	onAcceptFunc netConnect.OnAcceptFunc
	host         string
	port         uint16
	listener     net.Listener
	stopped      bool
}

// Listen 预绑定端口。绑定失败立即暴露给调用方，
// 避免异步 Start 失败被吞后向客户端返回"假成功"（报告 #8）。
//
// 修复 M-13：此前只判 `listener != nil`，而 Stop() 关了 listener 却不复位字段，
// 于是 Stop 之后 Listen() **恒返回 nil（声称端口可用，实际已关闭）**——
// inserver 建隧道的同步成功/失败应答正依赖这个返回值，等于报告 #8 的回归面。
// 现在按"已绑定且未关闭"判定，Stop 之后会真正重新绑定并如实报错。
func (this *AcceptorSocket) Listen() error {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.listener != nil && !this.stopped {
		return nil
	}
	l, err := net.Listen("tcp", net.JoinHostPort(this.host, strconv.Itoa(int(this.port))))
	if err != nil {
		return err
	}
	this.listener = l
	this.stopped = false
	return nil
}

// tempDelayMin / tempDelayMax 是 Accept 临时错误的退避区间，取值与标准库
// net/http 的 accept 循环一致。
const (
	tempDelayMin = 5 * time.Millisecond
	tempDelayMax = time.Second
)

func (this *AcceptorSocket) Start() error {
	this.mu.Lock()
	l := this.listener
	stopped := this.stopped
	accept := this.onAcceptFunc
	this.mu.Unlock()

	if stopped {
		return errors.New("acceptor: already stopped")
	}
	if l == nil {
		if err := this.Listen(); err != nil {
			return err
		}
		this.mu.Lock()
		l = this.listener
		this.mu.Unlock()
		if l == nil {
			return errors.New("acceptor: listener is nil after Listen")
		}
	}

	// 注意：循环内**不再**重读 this.listener、也不再调 Listen()。
	// Stop() 会把该字段置 nil，若在循环里"发现为 nil 就重绑"，Start 会自己
	// 把端口重新占上并继续 accept —— Stop 就此失去终止语义。
	tempDelay := time.Duration(0)
	for {
		conn, err := l.Accept()
		if err != nil {
			this.mu.Lock()
			stopped := this.stopped
			this.mu.Unlock()
			// 被 Stop()/Close() 关掉是正常退出路径，必须把错误回给调用方
			// （TestNetServer_Stop_ClosesAllSessions 依赖"Stop 后 Start 立即返回"）。
			if stopped || errors.Is(err, net.ErrClosed) {
				return err
			}
			// 其余是临时错误（fd 耗尽、协议栈瞬时错误）。此前这里无条件 return，
			// 一次错误就永久终止 accept 循环 → 该隧道静默死亡，而控制通道看起来
			// 仍然健康，也没有任何人去重新监听（报告 ROBUST-02）。改为退避重试。
			if tempDelay == 0 {
				tempDelay = tempDelayMin
			} else {
				tempDelay *= 2
			}
			if tempDelay > tempDelayMax {
				tempDelay = tempDelayMax
			}
			if lg := log.Main(); lg != nil {
				lg.Error("accept error, retry in %v: %v", tempDelay, err)
			}
			time.Sleep(tempDelay)
			continue
		}
		tempDelay = 0

		tcpConn, ok := conn.(*net.TCPConn)
		if ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}
		c := newConn(conn)
		if accept == nil {
			// 未接 onAccept 就断开，避免白起两个 goroutine
			c.Abort()
			continue
		}
		this.safeAccept(c, accept)
		go c.receiverRun()
		go c.senderRun()
	}
}

// Stop 关闭 listener 并**复位**内部状态，使后续 Listen()/Start() 重新真正绑定。
// 关闭错误不再被吞掉。
func (this *AcceptorSocket) Stop() error {
	this.mu.Lock()
	l := this.listener
	this.listener = nil
	this.stopped = true
	this.mu.Unlock()
	if l == nil {
		return nil
	}
	return l.Close()
}

func (this *AcceptorSocket) SetOnAccept(onAcceptFunc netConnect.OnAcceptFunc) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.onAcceptFunc = onAcceptFunc
}

func (this *AcceptorSocket) SetAddress(host string, port uint16) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.host = host
	this.port = port
}

// safeAccept 兜底 onAccept 回调中的 panic：任何上层 bug 都不允许
// 杀死 accept 循环乃至整个进程（项目无全局 recover，报告 #1/#2 的放大器）。
// log.Main() 可能未初始化（nil），不能让兜底路径自身 panic。
func (this *AcceptorSocket) safeAccept(c *ConnSocket, accept netConnect.OnAcceptFunc) {
	defer func() {
		if r := recover(); r != nil {
			if l := log.Main(); l != nil {
				l.Error("accept %v panic: %v", c.RemoteAddr(), r)
			}
			c.Abort()
		}
	}()
	accept(c)
}
