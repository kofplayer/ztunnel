package server

import (
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	netCodec "ztunnel/engine/net/codec"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// startOnPort 起一个透传编解码（codec_data）的 NetServer 并连上一条客户端连接。
//
// 返回的 handled 通道由 onMessage 在**服务端会话已建立、消息已进入业务回调**后
// 收到信号。必须先等这个信号再断言"会话已被回收"——否则 Eventually 会在
// 会话还没创建时就因 Len()==0 真空成立，测试失去意义。
func startOnPort(t *testing.T,
	onMessage func(s netSession.NetSession, cb, msgID uint32, data []byte) error,
	onDisconnect func(netSession.NetSession)) (NetServer, net.Conn, chan netSession.NetSession) {
	t.Helper()

	handled := make(chan netSession.NetSession, 4)
	wrapped := func(s netSession.NetSession, cb, msgID uint32, data []byte) error {
		select {
		case handled <- s:
		default:
		}
		return onMessage(s, cb, msgID, data)
	}

	port := testutil.FreePort(t)
	svr := NewNetServer()
	acc := socketNetConnect.NewAcceptor()
	acc.SetAddress("127.0.0.1", uint16(port))
	svr.SetAcceptor(acc)
	svr.SetCodec(netCodec.NewCodec_data())
	svr.SetOnMessage(wrapped)
	svr.SetOnDisconnect(onDisconnect)
	go func() { _ = svr.Start() }()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	var client net.Conn
	if !testutil.Eventually(t, 3*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		client = c
		return true
	}) {
		t.Fatal("服务端未就绪")
	}
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
		_ = svr.Stop()
	})
	return svr, client, handled
}

func mustWrite(t *testing.T, c net.Conn, b []byte) {
	t.Helper()
	_, err := c.Write(b)
	testutil.NoError(t, err)
}

// waitHandled 确认服务端已建立会话并进入业务回调。
func waitHandled(t *testing.T, handled chan netSession.NetSession) netSession.NetSession {
	t.Helper()
	select {
	case s := <-handled:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("服务端未把消息派发到 onMessage（会话未建立？）")
		return nil
	}
}

// 回归 SEC-01（主动关闭路径）：调用方在 OnMessage 内做 s.Close() —— 这正是
// common/server 在 handler 返回 error 时的行为 —— 发送队列会先被关掉，此前
// receiverRun 的 `if !q.IsClose()` 守卫据此跳过了整条 OnDisconnect 链，
// 于是唯一的清理点 RemoveSession 永不执行，会话永久留在 map 里。
func TestNetServer_ActiveClose_FiresDisconnectAndRemoves(t *testing.T) {
	testutil.SilentLog(t)

	var fired atomic.Int32
	svr, client, handled := startOnPort(t,
		func(s netSession.NetSession, _ uint32, _ uint32, _ []byte) error {
			_ = s.Close()
			return nil
		},
		func(netSession.NetSession) { fired.Add(1) })

	mustWrite(t, client, []byte{0xAB})
	waitHandled(t, handled)

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("SEC-01 未修复：主动 Close 后会话仍留在 sessionMgr（Len=%d），期望 0",
			svr.GetSessionMgr().Len()))
	testutil.Equal(t, 1, int(fired.Load()), "OnDisconnect 必须恰好执行 1 次（不是 0 次也不是 2 次）")
}

// 回归 SEC-01（错误关闭路径）：OnMessage 返回 error 时同样必须派发断开通知
// 并回收会话。
func TestNetServer_OnMessageError_RemovesSession(t *testing.T) {
	testutil.SilentLog(t)

	var fired atomic.Int32
	svr, client, handled := startOnPort(t,
		func(netSession.NetSession, uint32, uint32, []byte) error {
			return fmt.Errorf("boom")
		},
		func(netSession.NetSession) { fired.Add(1) })

	mustWrite(t, client, []byte{0xAB})
	waitHandled(t, handled)

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("SEC-01 未修复：OnMessage 报错后会话仍在 sessionMgr（Len=%d）",
			svr.GetSessionMgr().Len()))
	testutil.Equal(t, 1, int(fired.Load()), "OnDisconnect 应恰好执行 1 次")
}

// 回归 SEC-01（Stop 路径）：Stop 关闭全部存量会话后 map 必须清空，
// 且每个会话的 OnDisconnect 恰好一次。此前 Stop 只 Close、不派发，
// 导致 outserver 永远不会把 ConnectDelete 发给 client（跨进程泄漏）。
func TestNetServer_Stop_EmptsMapAndFiresOnce(t *testing.T) {
	testutil.SilentLog(t)

	var fired atomic.Int32
	accepted := make(chan struct{}, 1)
	port := testutil.FreePort(t)

	svr := NewNetServer()
	acc := socketNetConnect.NewAcceptor()
	acc.SetAddress("127.0.0.1", uint16(port))
	svr.SetAcceptor(acc)
	svr.SetCodec(netCodec.NewCodec_data())
	svr.SetOnAccept(func(netSession.NetSession) { accepted <- struct{}{} })
	svr.SetOnMessage(func(netSession.NetSession, uint32, uint32, []byte) error { return nil })
	svr.SetOnDisconnect(func(netSession.NetSession) { fired.Add(1) })
	go func() { _ = svr.Start() }()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	var client net.Conn
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		client = c
		return true
	}), "服务端未就绪")
	defer client.Close()

	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("服务端未 accept 连接")
	}
	// 会话注册发生在 accept 回调内，等到计数稳定再断言
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 1
	}), "用户会话未注册")

	testutil.NoError(t, svr.Stop())

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("SEC-01 未修复：Stop 后 sessionMgr 仍有 %d 个条目", svr.GetSessionMgr().Len()))
	testutil.Equal(t, 1, int(fired.Load()), "Stop 路径应派发恰好一次 OnDisconnect")

	// Stop 必须可重复调用且不 panic
	testutil.NoError(t, svr.Stop(), "Stop 应幂等")
}

// 回归 SEC-01 与 CRASH-01 的耦合点：OnDisconnect 回调内必须仍能取到 Conn，
// 否则像 inserver 那样用 s.GetConn().RemoteAddr() 记日志会 nil panic。
func TestNetServer_OnDisconnect_CanStillReadConn(t *testing.T) {
	testutil.SilentLog(t)

	var panicVal any
	panicDone := make(chan struct{})
	svr, client, handled := startOnPort(t,
		func(s netSession.NetSession, _ uint32, _ uint32, _ []byte) error {
			_ = s.Close()
			return nil
		},
		func(s netSession.NetSession) {
			defer func() {
				panicVal = recover()
				close(panicDone)
			}()
			_ = s.GetConn().RemoteAddr()
		})

	mustWrite(t, client, []byte{0xAB})
	waitHandled(t, handled)

	select {
	case <-panicDone:
	case <-time.After(3 * time.Second):
		t.Fatal("OnDisconnect 未被调用")
	}
	testutil.True(t, panicVal == nil,
		"CRASH-01 未修复：OnDisconnect 内 GetConn() 为 nil → panic", panicVal)

	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	}), "会话未被回收")
}
