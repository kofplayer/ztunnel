package socketNetConnect

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	zlog "ztunnel/engine/log"
	netConnect "ztunnel/engine/net/connect"
	"ztunnel/testutil"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// nilAddrConn 的 RemoteAddr() 返回 nil，用于覆盖防御分支。
type nilAddrConn struct {
	net.Conn
}

func (nilAddrConn) RemoteAddr() net.Addr { return nil }
func (nilAddrConn) Close() error         { return nil }

// flakyListener 的前 n 次 Accept 返回"临时错误"，之后阻塞直到 Close；
// Close 之后返回 wrapped net.ErrClosed，以便驱动 accept 循环的正常终止路径。
// 用来覆盖 accept 循环的退避重试分支（真实场景是 fd 耗尽等）。
type flakyListener struct {
	failures int
	calls    atomic.Int32
	stop     chan struct{}
	once     sync.Once
}

func (l *flakyListener) Accept() (net.Conn, error) {
	select {
	case <-l.stop:
		return nil, fmt.Errorf("accept: %w", net.ErrClosed)
	default:
	}
	if int(l.calls.Add(1)) <= l.failures {
		return nil, errors.New("temporary accept failure")
	}
	<-l.stop
	return nil, fmt.Errorf("accept: %w", net.ErrClosed)
}
func (l *flakyListener) Close() error {
	l.once.Do(func() { close(l.stop) })
	return nil
}
func (l *flakyListener) Addr() net.Addr { return nil }

// ---------------------------------------------------------------------------
// ConnSocket 防御分支
// ---------------------------------------------------------------------------

// RemoteAddr 在 conn 尚未挂上、或对端地址取不到时必须返回空串而不是 panic。
func TestConn_RemoteAddr_NilGuards(t *testing.T) {
	testutil.SilentLog(t)

	c := newConn(nil)
	testutil.Equal(t, "", c.RemoteAddr(), "conn 为 nil 时应返回空串")

	c2 := newConn(nilAddrConn{})
	testutil.Equal(t, "", c2.RemoteAddr(), "RemoteAddr 返回 nil 时应返回空串")
}

// 发送队列里混入非 []byte 属于上层接线 bug，必须按"连接异常关闭"处理：
// 关闭队列与 socket 并派发断开通知，而不是静默 return（那会同时泄漏 fd
// 并让 SendData 继续恒报成功）。
func TestConn_SenderRun_NonBytePayload_AbortsConn(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { return nil })

	go c.senderRun()

	// 绕过 SendData 直接塞一个非 []byte 进队列
	testutil.NoError(t, c.q.Enqueue("not a byte slice"))

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return fired.Load() == 1 && fc.closeCount() >= 1
	})
	testutil.True(t, ok,
		"非法载荷未被按连接异常处理: fired=", fired.Load(), " closeCount=", fc.closeCount())

	// 队列必须已关闭
	testutil.Error(t, c.SendData([]byte("x")), "Abort 之后发送应报错")
}

// 接收循环内 panic 必须被 recover 兜住，并按连接断开处理
// （关闭 socket + 派发断开通知），且不得杀死进程。
func TestConn_ReceiverPanic_ClosesConnAndNotifies(t *testing.T) {
	testutil.SilentLog(t)

	local, peer := net.Pipe()
	defer peer.Close()

	c := newConn(local)
	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { panic("handler exploded") })

	go c.receiverRun()

	// 真送一个字节进去，让 panic 发生在 onDataFunc 而不是读错误分支
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	_, err := peer.Write([]byte{0xAB})
	testutil.NoError(t, err, "写入测试数据失败")

	ok := testutil.Eventually(t, 3*time.Second, func() bool { return fired.Load() == 1 })
	testutil.True(t, ok, "接收侧 panic 后未派发断开通知")

	// socket 必须被关闭：对端读到 EOF/错误
	_, err = peer.Read(make([]byte, 4))
	testutil.Error(t, err, "接收侧 panic 后未关闭底层 socket")
}

// 发送循环内 panic 同样要被关闭并通知。
func TestConn_SenderPanic_ClosesConnAndNotifies(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { return nil })

	// 用会在 Write 时 panic 的连接替换
	c.conn = panickingConn{}
	go c.senderRun()
	testutil.NoError(t, c.q.Enqueue([]byte("boom")))

	ok := testutil.Eventually(t, 3*time.Second, func() bool { return fired.Load() == 1 })
	testutil.True(t, ok, "发送侧 panic 后未派发断开通知")
}

type panickingConn struct {
	nilAddrConn
}

func (panickingConn) Write([]byte) (int, error) { panic("write exploded") }

// 兜底路径自身绝不 panic：log.Main() 为 nil 时（上层未初始化日志）
// recoverPanic 仍要完成关闭动作。
func TestLogSenderFault_NilMainLogDoesNotPanic(t *testing.T) {
	prev := zlog.Main()
	t.Cleanup(func() { zlog.SetMainLog(prev) })

	// 1) 日志未初始化：必须安静返回，不得 panic
	zlog.SetMainLog(nil)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("logSenderFault 在日志未初始化时 panic: %v", r)
			}
		}()
		logSenderFault("receiver", "1.2.3.4:5", "some value")
	}()

	// 2) 日志可用：覆盖真实写日志分支
	testutil.SilentLog(t)
	logSenderFault("receiver", "1.2.3.4:5", "with logger")
}

// ---------------------------------------------------------------------------
// AcceptorSocket 分支
// ---------------------------------------------------------------------------

// 已 Stop 之后 Start 必须直接返回错误，且不得把端口重新占上。
func TestAcceptor_StartAfterStopFails(t *testing.T) {
	testutil.SilentLog(t)

	a := NewAcceptor()
	port := uint16(testutil.FreePort(t))
	a.SetAddress("", port)
	testutil.NoError(t, a.Listen())
	testutil.NoError(t, a.Stop())

	testutil.Error(t, a.Start(), "Stop 之后 Start 应立即返回错误")
}

// accept 临时错误（非 ErrClosed）必须退避重试而不是终止循环：
// 此前一次错误就让隧道永久静默死亡（报告 ROBUST-02）。
func TestAcceptor_TemporaryAcceptErrorRetries(t *testing.T) {
	testutil.SilentLog(t)

	fl := &flakyListener{failures: 3, stop: make(chan struct{})}
	a := NewAcceptor()
	a.mu.Lock()
	a.listener = fl
	a.mu.Unlock()

	returned := make(chan error, 1)
	go func() { returned <- a.Start() }()

	ok := testutil.Eventually(t, 5*time.Second, func() bool {
		return fl.calls.Load() >= 4
	})
	testutil.True(t, ok,
		"accept 临时错误未被重试，循环只尝试了 ", fl.calls.Load(), " 次")

	_ = fl.Close()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("listener 关闭后 Start 未返回")
	}
}

// 未接 onAccept 时必须直接断开该连接，而不是白起两个 goroutine。
func TestAcceptor_NoOnAcceptAbortsConnection(t *testing.T) {
	testutil.SilentLog(t)

	// 让 acceptor 自己拥有 listener：若先手工占住同一端口，Listen 必然
	// EADDRINUSE、Start 直接返回，本用例就会"假通过"。
	port := uint16(testutil.FreePort(t))
	a := NewAcceptor()
	a.SetAddress("127.0.0.1", port)
	go func() { _ = a.Start() }()
	defer a.Stop()

	addr := "127.0.0.1:" + strconv.Itoa(int(port))
	if !testutil.Eventually(t, 5*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}) {
		t.Fatal("acceptor 未进入监听")
	}

	c, err := net.Dial("tcp", addr)
	testutil.NoError(t, err)
	defer c.Close()

	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = c.Read(make([]byte, 4))
	testutil.Error(t, err, "未接 onAccept 时连接应被服务端立即关闭")
}

// onAccept panic 不得杀死 accept 循环，且该连接要被关闭。
func TestAcceptor_OnAcceptPanicKeepsLoopAlive(t *testing.T) {
	testutil.SilentLog(t)

	var conns atomic.Int32
	port := uint16(testutil.FreePort(t))
	a := NewAcceptor()
	a.SetAddress("127.0.0.1", port)
	a.SetOnAccept(func(netConnect.Conn) {
		conns.Add(1)
		panic("onAccept exploded")
	})
	go func() { _ = a.Start() }()
	defer a.Stop()

	addr := "127.0.0.1:" + strconv.Itoa(int(port))
	if !testutil.Eventually(t, 5*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}) {
		t.Fatal("acceptor 未进入监听")
	}

	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		testutil.NoError(t, err)

		// 该连接必须被 panic 兜底关掉
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = c.Read(make([]byte, 4))
		testutil.Error(t, err, "onAccept panic 后该连接应被关闭")
		_ = c.Close()
	}

	// 第二条连接仍能被 accept，说明循环没死
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		return conns.Load() >= 2
	}), "accept 循环在 onAccept panic 后停止工作, 实际处理 ", conns.Load())
}

// safeAccept 在回调未 panic 时不得干扰正常连接；panic 时必须关闭该连接。
func TestAcceptor_SafeAcceptPanicBoundary(t *testing.T) {
	testutil.SilentLog(t)

	a := NewAcceptor()
	closed := 0

	fc := newErrConn()
	c := newConn(fc)
	c.SetOnDisconnect(func() { closed++ })
	a.safeAccept(c, func(netConnect.Conn) {})
	testutil.Equal(t, 0, closed, "正常回调不应触发断开通知")
	testutil.Equal(t, 0, fc.closeCount(), "正常回调不应关闭 socket")

	fc2 := newErrConn()
	c2 := newConn(fc2)
	c2.SetOnDisconnect(func() { closed++ })
	a.safeAccept(c2, func(netConnect.Conn) { panic("onAccept exploded") })
	testutil.Equal(t, 1, closed, "panic 回调应触发一次断开通知")
	testutil.True(t, fc2.closeCount() >= 1, "panic 后应关闭 socket")
}
