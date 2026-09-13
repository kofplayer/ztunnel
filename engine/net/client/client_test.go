package client

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	netCodec "ztunnel/engine/net/codec"
	socketNetConnect "ztunnel/engine/net/connect/socket"
	type0NetEncrypt "ztunnel/engine/net/middleware/encrypt/type0"
	"ztunnel/testutil"
)

// 回归 M-12：netClient 原先没有任何状态守卫。第二次 Connect() 会重建一条中间件链
// 并覆盖 lastMiddleware（主 goroutine 写、**旧**连接的 receiver goroutine 并发读），
// 再对同一个 ConnectorSocket 二次拨号覆盖底层 conn → 旧 fd 与两个 goroutine 永久
// 残留；而 onDisconnectFunc 被改指向新链，旧连接断开时的清理会打到新链上。
// 此前只靠"调用方每轮新建实例"侥幸规避，类型层面毫无约束。

// newClientToPointAt 造一个只有透传 codec、无中间件的 client。
// 这种链在连上之后**不会**触发 OnReady，因此 Connect() 会一直阻塞——
// 正好用来稳定观察"连接进行中"这个状态。
func newClientToPointAt(t *testing.T, addr string) *netClient {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	testutil.NoError(t, err, "测试地址应为 host:port")
	p, err := strconv.Atoi(portStr)
	testutil.NoError(t, err, "端口应为数字")

	c := NewNetClient().(*netClient)
	conn := socketNetConnect.NewConnector()
	conn.SetAddress(host, uint16(p))
	c.SetConnector(conn)
	c.SetCodec(netCodec.NewCodec_data())
	c.SetOnMessage(func(uint32, uint32, []byte) error { return nil })
	return c
}

// holdListener 起一个"接受但保持连接"的监听，模拟对端连上后静默不发包。
func holdListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err, "起监听")

	// held 被 accept goroutine 追加、被 t.Cleanup 遍历，必须加锁——
	// 否则测试自己就成了数据竞争的源头。
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		_ = ln.Close() // 先关 listener，让 accept 循环退出
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // 不关闭，保持半开状态
			mu.Unlock()
		}
	}()
	return ln
}

func TestNetClient_ConnectIsRejectedWhileConnecting(t *testing.T) {
	testutil.SilentLog(t)

	ln := holdListener(t)
	c := newClientToPointAt(t, ln.Addr().String())

	firstDone := make(chan error, 1)
	go func() { firstDone <- c.Connect() }()

	// 等第一次 Connect 真正进入 Connecting（链已建立、正等握手结果）
	if !testutil.Eventually(t, 5*time.Second, func() bool {
		return c.state.Load() == stateConnecting
	}) {
		t.Fatalf("未进入 Connecting 状态，state=%d", c.state.Load())
	}

	// 关键断言：第二次 Connect 必须被拒绝，而不是悄悄造出第二条链
	second := c.Connect()
	testutil.Error(t, second, "M-12 未修复：Connecting 状态下重复 Connect 未被拒绝")
	testutil.True(t, strings.Contains(second.Error(), "not idle"),
		"错误信息应指明是状态问题，实际", second)

	// 再来一次也必须被拒（不能因为刚失败过就放行）
	testutil.Error(t, c.Connect(), "重复 Connect 应持续被拒绝")
	testutil.Equal(t, stateConnecting, c.state.Load(), "被拒的 Connect 不得改动状态")

	testutil.NoError(t, c.Disconnect())
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Disconnect 后第一个 Connect 未返回，拨号 goroutine 会泄漏")
	}
}

// Connect 失败必须落**终态**，而不是回 Idle 留下"重试"的后门：
// 拨号可能已建立了半开连接，再 Connect 一次就是对同一个 ConnectorSocket 二次
// 拨号并覆盖底层 conn（那正是 M-12 要防的）。
func TestNetClient_ConnectFailureIsTerminal(t *testing.T) {
	testutil.SilentLog(t)

	c := newClientToPointAt(t, "127.0.0.1:1") // 基本不可能有服务

	err := c.Connect()
	testutil.Error(t, err, "拨号不通时 Connect 应报错")
	testutil.Equal(t, stateDone, c.state.Load(),
		"M-12：Connect 失败必须落终态，不能留下可重试的 Idle")

	err2 := c.Connect()
	testutil.Error(t, err2, "已失败的实例不得再 Connect")
	testutil.True(t, strings.Contains(err2.Error(), "not idle"),
		"错误应指明状态问题（提示需构造新实例），实际", err2)
}

// 从未 Connect 过的实例上 Disconnect 也不应把状态弄乱。
func TestNetClient_DisconnectOnIdleIsSafe(t *testing.T) {
	testutil.SilentLog(t)

	c := newClientToPointAt(t, "127.0.0.1:1")
	_ = c.Disconnect()
	testutil.Equal(t, stateIdle, c.state.Load(), "未 Connect 就 Disconnect 后仍应为 Idle")
}

// 成功路径：接上 type0 中间件（它在 OnConnect 时立即 FireEvent(OnReady)），
// Connect 才能真正返回 nil 并进入 Ready —— 同时覆盖 AddMiddleware / SetOnConnect /
// SetOnReady / SetOnDisconnect 与 connect() 的成功分支。
func TestNetClient_FullLifecycleWithHandshake(t *testing.T) {
	testutil.SilentLog(t)

	ln := holdListener(t)
	c := newClientToPointAt(t, ln.Addr().String())
	c.AddMiddleware(type0NetEncrypt.NewClientNetEncrypt)

	var mu sync.Mutex
	var gotConnect, gotReady, gotDisconnect int
	c.SetOnConnect(func() { mu.Lock(); gotConnect++; mu.Unlock() })
	c.SetOnReady(func() { mu.Lock(); gotReady++; mu.Unlock() })
	c.SetOnDisconnect(func() { mu.Lock(); gotDisconnect++; mu.Unlock() })

	testutil.NoError(t, c.Connect(), "接上 type0 后握手应立即完成")
	testutil.Equal(t, stateReady, c.state.Load(), "成功握手后应进入 Ready")

	// 生命周期回调都应被触发
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gotConnect == 1 && gotReady == 1
	}), "OnConnect/OnReady 应各被调用一次")

	// Ready 状态下再 Connect 必须被拒
	testutil.Error(t, c.Connect(), "Ready 状态下重复 Connect 应被拒绝")

	// SendMessage 现在应当可用
	testutil.NoError(t, c.SendMessage(0, 1, []byte("hi")), "Ready 状态下 SendMessage 应成功")

	// 断开：状态落终态、onDisconnect 被派发一次
	testutil.NoError(t, c.Disconnect())
	testutil.Equal(t, stateDone, c.state.Load(), "Disconnect 后应落终态 Done")
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gotDisconnect == 1
	}), "断开回调应恰好被调用一次")

	// 单实例单连接：重连必须构造新实例
	err := c.Connect()
	testutil.Error(t, err, "断开后不得再复用同一个实例")
	testutil.True(t, strings.Contains(err.Error(), "new client"),
		"错误应指引调用方构造新实例，实际", err)
}

// SendMessage 在 Connect 之前调用必须报错而不是 nil 接口 panic（报告 L-12）。
func TestNetClient_SendMessageBeforeConnect(t *testing.T) {
	testutil.SilentLog(t)

	c := newClientToPointAt(t, "127.0.0.1:1")
	testutil.Error(t, c.SendMessage(0, 1, []byte("x")),
		"L-12 未修复：未连接就 SendMessage 会 nil 接口 panic")
}
