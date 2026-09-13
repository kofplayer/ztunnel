package inclient_test

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"ztunnel/client/inclient"
	"ztunnel/engine/net/client"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// acceptAndClose 起一个"accept 后立刻关闭"的服务，模拟内网服务在连接建立后
// 马上断开（MySQL max_connections 拒绝、LB 健康检查、`nc -z` 探测都是常态）。
func acceptAndClose(t *testing.T) (host string, port uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), uint16(addr.Port)
}

// 回归 M-15：`OpenClient` 此前是"先 Connect() 成功、后写 map"。
// Connect() 返回时该连接的 receiver goroutine 已经在跑——若内网服务 accept 后
// 立刻关闭，OnDisconnect → RemoveClient 会先删掉一个**还不存在**的条目，
// 随后死客户端被写回 map 并永久驻留：该 connectId 的用户数据从此静默丢弃。
// 文档说"窗口极小"，实际触发条件相当常见。
func TestClientMgr_OpenClient_NoDeadEntryWhenServiceClosesImmediately(t *testing.T) {
	testutil.SilentLog(t)

	host, port := acceptAndClose(t)
	mgr := inclient.NewClientMgr()
	outcli := client.NewNetClient() // 未连接的桩：SendMessage 会返回 error 而非 panic

	for i := 0; i < 10; i++ {
		id := netSession.SessionID(1000 + i)
		_, _ = mgr.OpenClient(id, host, port, outcli)
	}

	ok := testutil.Eventually(t, 5*time.Second, func() bool {
		return mgr.Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("M-15 未修复：对端立即关闭后 map 里残留了 %d 个死客户端条目", mgr.Len()))
}

// 回归 M-03 的一部分：同 connectId 再次 OpenClient 时，必须先关掉老连接，
// 且老连接的 OnDisconnect 不得误删新登记的条目（身份核对）。
func TestClientMgr_ReopenSameId_ReplacesOldButKeepsNew(t *testing.T) {
	testutil.SilentLog(t)

	// 一个 accept 后保持住的老实服务
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln1.Close()
	go func() {
		for {
			c, err := ln1.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(io.Discard, c) }(c)
		}
	}()
	addr1 := ln1.Addr().(*net.TCPAddr)

	mgr := inclient.NewClientMgr()
	outcli := client.NewNetClient()
	id := netSession.SessionID(7)

	old, err := mgr.OpenClient(id, addr1.IP.String(), uint16(addr1.Port), outcli)
	testutil.NoError(t, err)
	testutil.Equal(t, 1, mgr.Len())

	host2, port2 := acceptAndClose(t)
	fresh, err := mgr.OpenClient(id, host2, port2, outcli)
	// 第二个服务 accept 后立即关闭，OpenClient 可能返回错误；
	// 无论成败，登记与替换的语义都必须正确。
	if err == nil {
		testutil.True(t, fresh != old, "同 id 重开应产生新的客户端对象")
	}
	testutil.Equal(t, 1, mgr.Len(), "同 id 重开不得在 map 里留下两份")
}

// 回归本次修复**新引入**的风险（务必守住）：
// SEC-01 修复后，Disconnect() 会在调用 goroutine 内**同步**派发 OnDisconnect，
// 而 inclient.handler.OnDisconnect 会回调 RemoveClient —— 那要拿 ClientMgr 的 mu。
// 因此 CloseAllClient / OpenClient 若在**持 m.mu 时**调用 Disconnect 就会
// 同 goroutine 自死锁。这里用超时看门狗钉住这条重入路径。
func TestClientMgr_CloseAllClient_DoesNotDeadlock(t *testing.T) {
	testutil.SilentLog(t)

	host, port := acceptAndClose(t)
	mgr := inclient.NewClientMgr()
	outcli := client.NewNetClient()
	for i := 0; i < 5; i++ {
		_, _ = mgr.OpenClient(netSession.SessionID(2000+i), host, port, outcli)
	}

	done := make(chan struct{})
	go func() {
		mgr.CloseAllClient()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAllClient 死锁：持 ClientMgr.mu 期间调 Disconnect，" +
			"断开通知重入 RemoveClient 再次取同一把锁")
	}
	testutil.Equal(t, 0, mgr.Len())
}

// Stop/Close 路径不得留下条目。
func TestClientMgr_CloseClient_CleansMap(t *testing.T) {
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

	mgr := inclient.NewClientMgr()
	id := netSession.SessionID(4242)
	_, err = mgr.OpenClient(id, addr.IP.String(), uint16(addr.Port), client.NewNetClient())
	testutil.NoError(t, err)
	testutil.Equal(t, 1, mgr.Len())

	mgr.CloseClient(id)
	testutil.Equal(t, 0, mgr.Len())
	testutil.True(t, mgr.GetClient(id) == nil, "关闭后应查不到")
}
