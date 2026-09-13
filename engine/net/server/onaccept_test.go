package server

import (
	"net"
	"strconv"
	"testing"
	"time"

	netCodec "ztunnel/engine/net/codec"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// 回归 M-14：onAccept 回调 panic 时，NewSession() 已经把会话注册进了 map
// （它在 onAccept 之前执行），若 panic 后不做回收就会留下一个永久孤儿条目，
// 其 bindObject 会链住整个 outserver 对象图。
//
// 结论：SEC-01 的修复顺带解决了它 —— safeAccept 的 recover 调 Abort()，
// 而 Abort() 现在会幂等派发断开通知，通知闭包里就有 RemoveSession。
// 本用例把这个行为钉住，防止将来有人把 Abort 的通知语义改回去。
func TestNetServer_OnAcceptPanic_ReclaimsSession(t *testing.T) {
	testutil.SilentLog(t)

	port := testutil.FreePort(t)
	svr := NewNetServer()
	acc := socketNetConnect.NewAcceptor()
	acc.SetAddress("127.0.0.1", uint16(port))
	svr.SetAcceptor(acc)
	svr.SetCodec(netCodec.NewCodec_data())
	svr.SetOnMessage(func(netSession.NetSession, uint32, uint32, []byte) error { return nil })
	svr.SetOnAccept(func(netSession.NetSession) { panic("onAccept exploded") })

	go func() { _ = svr.Start() }()
	t.Cleanup(func() { _ = svr.Stop() })

	addr := "127.0.0.1:" + strconv.Itoa(port)
	var conn net.Conn
	testutil.True(t, testutil.Eventually(t, 5*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		conn = c
		return true
	}), "服务端未就绪")
	defer conn.Close()

	// 会话曾经被创建（onAccept 在 NewSession 之后才 panic），panic 后必须被回收
	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		"M-14 未解决：onAccept panic 后仍有孤儿会话留在 map 中, Len=",
		svr.GetSessionMgr().Len())
}
