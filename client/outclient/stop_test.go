package outclient

import (
	"io"
	"net"
	"testing"
	"time"

	"ztunnel/common/proto"
	"ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	netConnect "ztunnel/engine/net/connect"
	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// 回归 LEAK-03：`Stop()` 此前只向 c.c 发信号 + 关 inclient，
// **从不 Disconnect 控制连接**。于是"建隧道 10s 超时"这一最常见路径会留下一条
// ESTABLISHED 的控制连接：它的 receiver/sender goroutine 常驻，服务端侧会话也
// 认为对端仍活着并继续持有 export 端口；外层重连循环随后新建 client →
// 该端口 EADDRINUSE → 再泄一条。**每轮重连泄一条，永不自愈。**
//
// 断言方式：让假服务端把控制连接读到 EOF。Stop 之后服务端必须立刻读到 EOF，
// 说明 TCP 真的被客户端关掉了。
func TestOutClient_Stop_DisconnectsControlConn(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("")
	t.Cleanup(func() { proto.SetToken("") })
	proto.NetEncrypt = false

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln.Close()

	serverSawEOF := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverSawEOF <- err
			return
		}
		// 读到 EOF 或错误即代表客户端真正关闭了连接
		_, err = io.Copy(io.Discard, c)
		serverSawEOF <- err
	}()

	addr := ln.Addr().(*net.TCPAddr)
	cli := NewClient("127.0.0.1", uint16(addr.Port), 9999, "127.0.0.1", 1)

	startDone := make(chan error, 1)
	go func() { startDone <- cli.Start() }()

	// 等控制连接建立（Start 内部 Connect 成功后会阻塞在 <-c.c）
	time.Sleep(300 * time.Millisecond)

	// Stop 必须在通知超时的同时把控制连接真正关掉
	_ = cli.Stop()

	select {
	case err := <-serverSawEOF:
		// io.Copy 正常结束返回 io.EOF
		testutil.True(t, err == nil || err == io.EOF,
			"服务端读取控制连接出现意外错误", err)
	case <-time.After(5 * time.Second):
		t.Fatal("LEAK-03 未修复：Stop() 之后控制连接仍未被关闭，" +
			"服务端还在等数据 → 该连接及其 goroutine 永久泄漏，export 端口被僵尸会话钉住")
	}

	select {
	case <-startDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop 之后 Start() 未返回")
	}
}

// Stop 必须可被重复调用（超时回调、失败应答、重连循环都会调它），
// 不得因通道满而阻塞调用方。
func TestOutClient_StopIsIdempotent(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("")
	proto.NetEncrypt = false

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_, _ = io.Copy(io.Discard, c)
			_ = c.Close()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	cli := NewClient("127.0.0.1", uint16(addr.Port), 9999, "127.0.0.1", 1)
	go func() { _ = cli.Start() }()
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10; i++ {
			_ = cli.Stop()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("重复 Stop() 阻塞：通道发送未做成非阻塞")
	}
}

// 回归 M-01：ConnectNew 应答此前把收到的**整个** payload 原样回显
// （`append([]byte{code}, data...)`），而服务端要求该帧长度恒为 SessionIDSize+1。
// 对端只要把 ConnectNew 写得长一点（版本演进、实现差异、明文通道注入），
// 客户端就会亲手把长度放大回去、让自己的隧道被判死并断开。
func TestOutClient_ConnectNewReply_HasExactWidth(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	var got [][]byte
	h := newTestHandler()
	h.outCli.cli = &recordingClient{sent: &got}

	// 故意发来一个超长的 ConnectNew（带 8 字节"额外"数据）
	long := []byte{0, 0, 0, 1, 2, 3, 4, 9, 9, 9, 9, 9}
	// forward 端口不可达 → code 必为 ErrorCodeFailed，但应答宽度必须正确
	_ = h.OnMessage(0, proto.MsgIdConnectNew, long)

	testutil.True(t, len(got) == 1, "应回出一帧", len(got))
	testutil.Equal(t, netSessionSize+1, len(got[0]),
		"M-01 未修复：ConnectNew 应答把对端的多余字节原样回显了，服务端会判协议错误并断开")
}

const netSessionSize = 4

// recordingClient 是 client.NetClient 的替身：只记录 SendMessage 的载荷。
type recordingClient struct {
	sent             *[][]byte
	client.NetClient // 借用 nil 接口取得其余方法签名；被意外调用时会 panic
}

func (r *recordingClient) Connect() error { return nil }

func (r *recordingClient) Disconnect() error { return nil }

func (r *recordingClient) SendMessage(cb uint32, msgID uint32, data []byte) error {
	*r.sent = append(*r.sent, append([]byte(nil), data...))
	return nil
}

func (r *recordingClient) SetConnector(netConnect.Connector)               {}
func (r *recordingClient) SetCodec(netCodec.Codec)                         {}
func (r *recordingClient) SetOnConnect(func())                             {}
func (r *recordingClient) SetOnReady(func())                               {}
func (r *recordingClient) SetOnDisconnect(func())                          {}
func (r *recordingClient) SetOnMessage(func(uint32, uint32, []byte) error) {}
func (r *recordingClient) AddMiddleware(func() netMiddleware.Middleware)   {}
