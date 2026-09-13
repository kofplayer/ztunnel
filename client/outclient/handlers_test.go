package outclient

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"ztunnel/client/inclient"
	"ztunnel/common/proto"
	netClient "ztunnel/engine/net/client"
	netCodec "ztunnel/engine/net/codec"
	netConnect "ztunnel/engine/net/connect"
	netMiddleware "ztunnel/engine/net/middleware"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// failingClient 的控制通道桩：SendMessage 固定返回错误，用来验证
// "写失败必须可见"（报告 M-18）而不是被静默吞掉。
type failingClient struct {
	netClient.NetClient // 借方法签名；未覆写的方法被调用即 nil panic
	err                 error
	calls               int
}

func (f *failingClient) Connect() error    { return nil }
func (f *failingClient) Disconnect() error { return nil }
func (f *failingClient) SendMessage(uint32, uint32, []byte) error {
	f.calls++
	return f.err
}
func (f *failingClient) SetConnector(netConnect.Connector)               {}
func (f *failingClient) SetCodec(netCodec.Codec)                         {}
func (f *failingClient) SetOnConnect(func())                             {}
func (f *failingClient) SetOnReady(func())                               {}
func (f *failingClient) SetOnDisconnect(func())                          {}
func (f *failingClient) SetOnMessage(func(uint32, uint32, []byte) error) {}
func (f *failingClient) AddMiddleware(func() netMiddleware.Middleware)   {}

func idBytes(id netSession.SessionID) []byte {
	b := make([]byte, netSession.SessionIDSize)
	proto.WriteSessionId(b, id)
	return b
}

// ---------------------------------------------------------------------------
// CreateTunnel 应答的三个分支（此前只在崩溃探针子进程里跑过，父用例不计覆盖）
// ---------------------------------------------------------------------------

func TestOutClient_CreateTunnel_EmptyResponseStopsClient(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	h := newTestHandler()
	h.outCli.cli = &failingClient{err: errors.New("x")}

	// 空应答必须报错而不是 data[0] 越界 panic（报告 #14）
	err := h.OnMessage(0, proto.MsgIdCreateTunnel, []byte{})
	testutil.Error(t, err, "空应答必须返回 error")
	testutil.Equal(t, 0, h.outCli.cli.(*failingClient).calls, "空应答不应再回发")

	// Stop 已把终止信号放入通道（非阻塞，可重复调用）
	select {
	case <-h.outCli.c:
	case <-time.After(time.Second):
		t.Fatal("空应答应已触发 Stop")
	}
}

func TestOutClient_CreateTunnel_ErrorCodeStopsClient(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()
	h.outCli.cli = &failingClient{err: errors.New("gone")}

	err := h.OnMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeFailed})
	testutil.Error(t, err, "服务端回错误码时必须报错以触发重连")

	select {
	case <-h.outCli.c:
	case <-time.After(time.Second):
		t.Fatal("失败应答应已触发 Stop")
	}
}

// 成功应答必须停掉并清空建隧道超时定时器（否则 10 秒后会误 Stop 一条已建好的隧道）。
func TestOutClient_CreateTunnel_SuccessClearsTimer(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()
	h.outCli.cli = &recordingClient{sent: new([][]byte)}

	// 用真实 Timer，delay 给长，确保不会因为超时而自然置 nil
	h.timer = time.AfterFunc(time.Hour, func() {})
	timer := h.timer

	testutil.NoError(t, h.OnMessage(0, proto.MsgIdCreateTunnel, []byte{proto.ErrorCodeNone}))

	h.timerMu.Lock()
	cleared := h.timer == nil
	h.timerMu.Unlock()
	testutil.True(t, cleared, "成功应答后定时器字段应被清空（说明已 Stop）")
	testutil.True(t, !timer.Stop(), "定时器应已被 Stop（Stop 返回 false 表示已失效/已停止）")

	// 成功路径不得发出终止信号
	select {
	case got := <-h.outCli.c:
		t.Fatalf("成功路径不应发终止信号, got %v", got)
	default:
	}
}

// ---------------------------------------------------------------------------
// ConnectNew 应答
// ---------------------------------------------------------------------------

// 转发建立失败时回 ErrorCodeFailed；控制通道写失败必须可见并上抛（M-18）。
func TestOutClient_ConnectNew_ReplyFailureIsReturned(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	h := newTestHandler()
	// forward 指向必然被拒的地址 → OpenClient 失败 → code=ErrorCodeFailed
	h.outCli.forwardHost, h.outCli.forwardPort = "127.0.0.1", 1
	h.outCli.cli = &failingClient{err: errors.New("control channel gone")}

	testutil.Error(t, h.OnMessage(0, proto.MsgIdConnectNew, idBytes(9)),
		"控制通道写失败必须上抛，让引擎关闭已死的隧道")
}

// 应答宽度必须是 1+4：多回显一个字节都会让服务端判协议错误并拆掉隧道（M-01）。
func TestOutClient_ConnectNew_ReplyWidth(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	var sent [][]byte
	h := newTestHandler()
	h.outCli.forwardHost, h.outCli.forwardPort = "127.0.0.1", 1
	h.outCli.cli = &recordingClient{sent: &sent}

	long := append(idBytes(11), 0xDE, 0xAD) // 故意超长
	testutil.NoError(t, h.OnMessage(0, proto.MsgIdConnectNew, long))

	testutil.Equal(t, 1, len(sent),
		"只应回出一帧 ConnectNew 应答；若出现第二帧，多半是 inclient 为这个"+
			"**从未建立**的转发补发了 ConnectDelete（拨号失败不应派发断开通知，"+
			"那会让服务端凭空多收一帧删除消息）")
	testutil.Equal(t, netSession.SessionIDSize+1, len(sent[0]),
		"M-01 未修复：应答把对端的多余字节原样回显了")
}

// ---------------------------------------------------------------------------
// ConnectData / ConnectDelete
// ---------------------------------------------------------------------------

// 未知 connectId 的数据必须留痕后跳过（不 panic、不误拆隧道）。
func TestOutClient_ConnectData_UnknownIdIsLoggedAndIgnored(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()
	h.outCli.cli = &recordingClient{sent: new([][]byte)}

	body := append(idBytes(999), 'a', 'b')
	testutil.NoError(t, h.OnMessage(0, proto.MsgIdConnectData, body),
		"未知 connectId 不应导致报错或 panic")
}

// 已登记的 inclient 收到数据必须剥掉 4 字节头部后转发给真实服务。
func TestOutClient_ConnectData_ForwardsToStrippedPayload(t *testing.T) {
	testutil.SilentLog(t)

	// 起一个真实的服务端充当"内网真实服务"，让 OpenClient 能成功登记
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(io.Discard, c) }(c)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)

	h := newTestHandler()
	h.outCli.forwardHost = "127.0.0.1"
	h.outCli.forwardPort = uint16(addr.Port)
	h.outCli.cli = &recordingClient{sent: new([][]byte)}

	id := netSession.SessionID(21)
	openRetry(t, h, id)
	t.Cleanup(func() { h.outCli.inClientMgr.CloseClient(id) })

	body := append(idBytes(id), "hello"[0:5]...)
	testutil.NoError(t, h.OnMessage(0, proto.MsgIdConnectData, body))

	// 转发后 inclient 仍登记在管理器里（没有被误回收）
	testutil.True(t, h.outCli.inClientMgr.GetClient(id) != nil, "合法转发不应回收该客户端")
}

// ConnectDelete 必须关掉对应转发并把它从登记表里移除。
func TestOutClient_ConnectDelete_ClosesAndUnregisters(t *testing.T) {
	testutil.SilentLog(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(io.Discard, c) }(c)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)

	h := newTestHandler()
	h.outCli.forwardHost = "127.0.0.1"
	h.outCli.forwardPort = uint16(addr.Port)
	h.outCli.cli = &recordingClient{sent: new([][]byte)}

	id := netSession.SessionID(31)
	openRetry(t, h, id)
	testutil.Equal(t, 1, h.outCli.inClientMgr.Len())

	testutil.NoError(t, h.OnMessage(0, proto.MsgIdConnectDelete, idBytes(id)))
	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return h.outCli.inClientMgr.Len() == 0
	})
	testutil.True(t, ok, "ConnectDelete 后登记表应清空, 仍有 ", h.outCli.inClientMgr.Len())
}

// ---------------------------------------------------------------------------
// Start / OnDisconnect 通道语义
// ---------------------------------------------------------------------------

// Connect 失败必须原样回传给调用方（外层重连循环靠它判断）。
func TestOutClient_StartReturnsConnectError(t *testing.T) {
	testutil.SilentLog(t)

	c := NewClient("127.0.0.1", 1, 9999, "127.0.0.1", 1) // 必然被拒
	done := make(chan error, 1)
	go func() { done <- c.Start() }()

	select {
	case err := <-done:
		testutil.Error(t, err, "拨号失败时 Start 应返回错误")
	case <-time.After(5 * time.Second):
		t.Fatal("Start 未返回")
	}
}

// 通道已满时 OnDisconnect 不得阻塞接收 goroutine（cap=2 只是隐式契约）。
func TestOutClient_OnDisconnectDoesNotBlockWhenChannelFull(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()

	// 先把 cap=2 的通道灌满
	h.outCli.c <- true
	h.outCli.c <- true

	done := make(chan struct{})
	go func() {
		h.OnDisconnect()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDisconnect 在通道满时阻塞了接收 goroutine，会令控制通道读循环停摆")
	}
}

// Stop 可被重复调用（超时回调、失败应答、重连循环都会调）。
func TestOutClient_StopRepeatedDoesNotBlock(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()
	c := &outClient{c: make(chan bool, 2), inClientMgr: inclient.NewClientMgr()}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			_ = c.Stop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("重复 Stop 阻塞")
	}
	_ = h
}

// openRetry 反复尝试登记一个转发客户端。
// 全套测试并发跑时真实拨号可能偶发失败，不加重试会让 CI 随机变红。
func openRetry(t *testing.T, h *handler, id netSession.SessionID) {
	t.Helper()
	for i := 0; i < 10; i++ {
		if _, err := h.outCli.inClientMgr.OpenClient(id, h.outCli.forwardHost,
			h.outCli.forwardPort, h.outCli.cli); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("OpenClient 连续 10 次失败")
}
