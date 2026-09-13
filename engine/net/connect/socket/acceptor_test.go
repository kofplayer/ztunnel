package socketNetConnect_test

import (
	"net"
	"strconv"
	"testing"
	"time"

	netConnect "ztunnel/engine/net/connect"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	"ztunnel/testutil"
)

// 回归 M-13：`Stop()` 关了 listener 却**不复位字段**，于是之后调用 `Listen()`
// 会走 `if this.listener != nil { return nil }` —— 恒返回"端口可用"的假成功，
// 而那个 listener 实际上早已关闭。inserver 建隧道的同步成功/失败应答正依赖这个
// 返回值（就是报告 #8 的回归面），Start() 也会拿着已关的 listener 立刻退出。
func TestAcceptor_StopThenListen_RebindsAndReportsTruth(t *testing.T) {
	testutil.SilentLog(t)

	port := testutil.FreePort(t)
	addr := "0.0.0.0:" + strconv.Itoa(port)

	a := socketNetConnect.NewAcceptor()
	// 必须绑通配：Windows 下"具体 IP"与"通配"绑定互不冲突，
	// 用 127.0.0.1 会让下面的端口占用断言失去意义（inserver_test 已记过这个坑）。
	a.SetAddress("", uint16(port))
	testutil.NoError(t, a.Listen())
	testutil.NoError(t, a.Stop())

	// 1) Stop 必须真的释放了端口
	occ, err := net.Listen("tcp", addr)
	testutil.NoError(t, err, "Stop 之后端口应已释放")

	// 2) 端口被占时 Listen 必须如实报错，而不是返回 nil 的"假成功"
	err = a.Listen()
	testutil.Error(t, err,
		"M-13 未修复：Stop 之后 Listen 对已被占用的端口仍返回成功（listener 未复位）")

	// 3) 端口空出来后应能真正重新绑定
	_ = occ.Close()
	if !testutil.Eventually(t, 3*time.Second, func() bool {
		return a.Listen() == nil
	}) {
		t.Fatal("Stop 之后无法重新绑定同一端口")
	}
	testutil.NoError(t, a.Stop())
}

// Stop 必须幂等，且不再吞掉关闭错误。
func TestAcceptor_StopIsIdempotent(t *testing.T) {
	testutil.SilentLog(t)

	a := socketNetConnect.NewAcceptor()
	a.SetAddress("127.0.0.1", uint16(testutil.FreePort(t)))
	testutil.NoError(t, a.Listen())
	testutil.NoError(t, a.Stop())
	testutil.NoError(t, a.Stop(), "重复 Stop 不应报错")
}

// Start 在 Stop 之后必须立即返回错误——既有测试依赖这一语义。
func TestAcceptor_StartReturnsAfterStop(t *testing.T) {
	testutil.SilentLog(t)

	a := socketNetConnect.NewAcceptor()
	port := uint16(testutil.FreePort(t))
	a.SetAddress("127.0.0.1", port)
	a.SetOnAccept(func(netConnect.Conn) {})
	testutil.NoError(t, a.Listen())

	started := make(chan error, 1)
	go func() { started <- a.Start() }()

	// 连一次，确保 accept 循环已就位
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(int(port)))
	testutil.NoError(t, err)
	_ = c.Close()

	testutil.NoError(t, a.Stop())
	select {
	case err := <-started:
		testutil.Error(t, err, "Stop 后 Start 应带着错误返回，让调用方知道循环已结束")
	case <-time.After(3 * time.Second):
		t.Fatal("Stop 之后 Start() 未返回（accept 循环未退出）")
	}
}
