package type1NetEncrypt

import (
	"net"
	"testing"
	"time"

	netMiddleware "ztunnel/engine/net/middleware"
	len4Data "ztunnel/engine/net/middleware/package/len4Data"
	netMiddlewareVerifier "ztunnel/engine/net/middleware/verifier"
	"ztunnel/testutil"
)

// clientChain / serverChain 复刻生产链路：len4 → type1 → verifier。
func clientChain(send func([]byte) error, onReceive func([]byte) error, onReady func()) (first, last netMiddleware.Middleware) {
	return testutil.BuildChain(send, []netMiddleware.CreateMiddlewareFunc{
		len4Data.NewMiddleware, NewClientNetEncrypt, netMiddlewareVerifier.NewMiddleware,
	}, onReceive, onReady)
}

func serverChain(send func([]byte) error, onReceive func([]byte) error, onReady func()) (first, last netMiddleware.Middleware) {
	return testutil.BuildChain(send, []netMiddleware.CreateMiddlewareFunc{
		len4Data.NewMiddleware, CreateServerNetEncryptFunc(), netMiddlewareVerifier.NewMiddleware,
	}, onReceive, onReady)
}

type encPair struct {
	cFirst, cLast netMiddleware.Middleware
	sFirst, sLast netMiddleware.Middleware
	crec, srec    *testutil.Recorder
	pumpErrC      <-chan error
	pumpErrS      <-chan error
}

// startPair 用 net.Pipe 建立一对完整握手成功的加密链路（客户端/服务端各一条）。
// 就绪判定来自中间件链自身的 OnReady 事件，非轮询。
func startPair(t *testing.T) *encPair {
	t.Helper()
	p := &encPair{
		crec: &testutil.Recorder{},
		srec: &testutil.Recorder{},
	}
	c1, s1 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = s1.Close()
	})

	cReady := make(chan struct{})
	sReady := make(chan struct{})
	cSend := func(b []byte) error { _, err := c1.Write(b); return err }
	sSend := func(b []byte) error { _, err := s1.Write(b); return err }
	p.cFirst, p.cLast = clientChain(cSend, p.crec.Receive, func() { close(cReady) })
	p.sFirst, p.sLast = serverChain(sSend, p.srec.Receive, func() { close(sReady) })
	// net.Pipe：写入 c1 的数据须从 s1 读出，写入 s1 的数据须从 c1 读出
	p.pumpErrC = testutil.Pump(c1, p.cFirst) // 客户端接收侧：读 c1（收到对端 s1 的写入）
	p.pumpErrS = testutil.Pump(s1, p.sFirst) // 服务端接收侧：读 s1（收到对端 c1 的写入）

	// 触发客户端握手（OnConnect → 发送 key1，双方自动完成 3 步交换）
	p.cFirst.FireEvent(netMiddleware.MiddlewareEventOnConnect)

	deadline := time.After(10 * time.Second)
	cReadyDone, sReadyDone := false, false
	for !cReadyDone || !sReadyDone {
		select {
		case <-cReady:
			cReadyDone = true
		case <-sReady:
			sReadyDone = true
		case err := <-p.pumpErrC:
			t.Fatalf("握手期间 client 链错误: %v", err)
		case err := <-p.pumpErrS:
			t.Fatalf("握手期间 server 链错误: %v", err)
		case <-deadline:
			t.Fatal("加密握手 10s 未完成")
		}
	}
	return p
}

// 基线：完整握手后双向数据加解密还原。
func TestType1_Handshake_Roundtrip(t *testing.T) {
	p := startPair(t)

	msg := []byte("hello encrypted tunnel")
	testutil.NoError(t, p.cLast.SendData(msg))
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool { return p.srec.Count() == 1 }),
		"server 未收到 client 消息")
	testutil.BytesEqual(t, msg, p.srec.Get(0))

	back := []byte("server says hi")
	testutil.NoError(t, p.sLast.SendData(back))
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool { return p.crec.Count() == 1 }),
		"client 未收到 server 消息")
	testutil.BytesEqual(t, back, p.crec.Get(0))
}

// 基线：畸形握手输入必须返回错误而非 panic。
// 注意必须以 len4 完整帧的形式喂入（裸短字节只会被分包层缓冲，不会到达加密层）。
func TestType1_Handshake_MalformedInputs(t *testing.T) {
	// client：握手第二步收到短数据（伪装 key2+pubkey）
	cFirst, _ := clientChain(func([]byte) error { return nil }, func([]byte) error { return nil }, nil)
	cFirst.FireEvent(netMiddleware.MiddlewareEventOnConnect)
	testutil.Error(t, cFirst.ReceiveData([]byte{0, 0, 0, 3, 1, 2, 3}), "client 应拒绝畸形的 key2+pubkey")

	// server：第一步收到错误长度的 key1
	sFirst, _ := serverChain(func([]byte) error { return nil }, func([]byte) error { return nil }, nil)
	testutil.Error(t, sFirst.ReceiveData([]byte{0, 0, 0, 3, 1, 2, 3}), "server 应拒绝错误长度的 key1")
}
