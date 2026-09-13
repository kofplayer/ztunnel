package socketNetConnect

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ztunnel/testutil"
)

// errConn 是最小可注入错误的 net.Conn 桩：只实现收发循环真正会调用的方法。
type errConn struct {
	// 嵌入 nil 接口以获得 net.Conn 其余方法的签名。真正被实现的只有
	// Read/Write/Close/RemoteAddr；若收发循环调用到别的方法就会 nil panic，
	// 那本身就是"实现与用法脱节"的信号。
	net.Conn

	mu       sync.Mutex
	writeErr error
	closeN   int

	readErr error

	// releaseOnce 保护 unblock 的关闭：Close() 与测试都可能触发它，
	// 用"先检查再关闭"仍有竞态，会偶发 close of closed channel。
	releaseOnce sync.Once
	unblock     chan struct{}

	remoteAd string
}

func newErrConn() *errConn {
	return &errConn{unblock: make(chan struct{}), remoteAd: "203.0.113.7:44444"}
}

// release 让阻塞中的 Read 立即返回。
func (c *errConn) release() {
	c.releaseOnce.Do(func() { close(c.unblock) })
}

func (c *errConn) setWriteErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeErr = err
}

func (c *errConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeN
}

func (c *errConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	err := c.writeErr
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *errConn) Read(b []byte) (int, error) {
	<-c.unblock
	c.mu.Lock()
	err := c.readErr
	c.mu.Unlock()
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (c *errConn) Close() error {
	c.mu.Lock()
	c.closeN++
	c.mu.Unlock()
	c.release() // Close 必须唤醒阻塞中的 Read
	return nil
}

func (c *errConn) RemoteAddr() net.Addr { return addrString(c.remoteAd) }
func (c *errConn) LocalAddr() net.Addr  { return addrString("0.0.0.0:0") }

type addrString string

func (a addrString) Network() string { return "tcp" }
func (a addrString) String() string  { return string(a) }

// 回归 LEAK-02：senderRun 的写失败分支此前只有一个裸 return——
// 既不关底层 conn（fd 永久泄漏，全仓库再无别处会关它），也不关队列
// （于是 SendData 继续"恒成功"，数据堆在无人消费的队列里），
// 更不通知业务（会话永久留在 sessionMgr，接收 goroutine 可无限期阻塞在 Read）。
func TestConn_WriteError_ClosesConnAndFiresDisconnect(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { return nil })

	go c.senderRun()

	fc.setWriteErr(errors.New("broken pipe"))
	testutil.NoError(t, c.SendData([]byte("payload")), "入队本身应成功")

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return fc.closeCount() >= 1 && fired.Load() == 1
	})
	testutil.True(t, ok,
		fmt.Sprintf("LEAK-02 未修复：写失败后未关闭 TCP（closeCount=%d）或未派发断开通知（fired=%d）",
			fc.closeCount(), fired.Load()))

	// 队列必须已关闭：否则业务会一直以为"发送成功"
	testutil.Error(t, c.SendData([]byte("more")), "LEAK-02 未修复：写失败后 SendData 仍恒返回成功")

	// 通知必须恰好一次（后续 receiver 的错误分支不得重复派发）
	time.Sleep(100 * time.Millisecond)
	testutil.Equal(t, 1, int(fired.Load()), "断开通知被重复派发")
}

// 回归 LEAK-02 的接收侧：receiverRun 读到错误时必须关闭队列并派发通知，
// 且与发送侧的并发通知只生效一次。
func TestConn_ReadError_FiresDisconnectOnce(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { return nil })

	go c.senderRun()
	go c.receiverRun()

	fc.release() // 让 Read 立刻返回 EOF
	// 主动关闭与被动读错误同时发生
	_ = c.Disconnect()

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return fired.Load() == 1
	})
	testutil.True(t, ok, fmt.Sprintf("断开通知应恰好一次，实际 fired=%d", fired.Load()))

	time.Sleep(150 * time.Millisecond)
	testutil.Equal(t, 1, int(fired.Load()), "SEC-01 未修复：断开通知被派发多次")
}

// 回归 SEC-01 的根因：断开通知不能用 q.IsClose() 当门。
// 先 Disconnect()（队列已关）再让 receiver 读到错误，通知仍必须发生。
func TestConn_DisconnectThenReadError_StillFires(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.SetOnData(func([]byte) error { return nil })

	go c.receiverRun()
	testutil.NoError(t, c.Disconnect())

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return fired.Load() == 1
	})
	testutil.True(t, ok, "SEC-01 未修复：队列先关导致 receiverRun 的 !q.IsClose() 守卫吞掉了断开通知")
}

// 断开回调尚未安装时触发 fireDisconnect，**不得**烧掉唯一一次通知机会。
// （accept 路径上 SetOnDisconnect 在 safeAccept 内安装，panic 可能发生在它之前。）
func TestConn_FireDisconnectBeforeCallbackInstalled(t *testing.T) {
	testutil.SilentLog(t)

	fc := newErrConn()
	c := newConn(fc)

	c.fireDisconnect() // 此刻回调为 nil

	var fired atomic.Int32
	c.SetOnDisconnect(func() { fired.Add(1) })
	c.fireDisconnect()
	c.fireDisconnect()

	testutil.Equal(t, 1, int(fired.Load()), "回调安装后必须仍能派发一次")
}
