package inclient

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	netClient "ztunnel/engine/net/client"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// newHandler 构造一个指向"未连接的控制通道"的转发处理器：
// 它的 SendMessage 必然失败，正好用来验证失败路径不再被静默吞掉（M-18）。
func newHandler(id netSession.SessionID) (*handler, *ClientMgr) {
	mgr := NewClientMgr()
	h := &handler{mgr: mgr, outcli: netClient.NewNetClient(), connectId: id}
	cli := netClient.NewNetClient()
	h.self = cli
	return h, mgr
}

// 真实服务断开时必须经控制通道上报 ConnectDelete，并按身份清掉登记表条目。
func TestInClient_OnDisconnect_ReportsAndUnregisters(t *testing.T) {
	testutil.SilentLog(t)

	id := netSession.SessionID(1234)
	h, mgr := newHandler(id)
	// 登记该客户端，才能验证"按身份删除"确实命中
	mgr.mu.Lock()
	mgr.clients[id] = h.self
	mgr.mu.Unlock()

	h.OnDisconnect() // 控制通道未连接：内部 SendMessage 会失败，但不得 panic

	testutil.Equal(t, 0, mgr.Len(), "OnDisconnect 后该条目应被移除")
}

// 身份核对：同 id 已被更新的客户端接管时，老连接的 OnDisconnect 不得删掉新条目。
// 这是 M-15/M-03 修复的关键——否则替换同 id 老连接会误杀新登记的转发。
func TestInClient_OnDisconnect_DoesNotEvictNewerClient(t *testing.T) {
	testutil.SilentLog(t)

	id := netSession.SessionID(4321)
	h, mgr := newHandler(id)
	mgr.mu.Lock()
	mgr.clients[id] = h.self
	mgr.mu.Unlock()

	// 模拟同 id 被新客户端接管（覆盖 map 条目，h.self 已不是登记对象）
	replacement := netClient.NewNetClient()
	mgr.mu.Lock()
	mgr.clients[id] = replacement
	mgr.mu.Unlock()

	h.OnDisconnect()

	testutil.Equal(t, 1, mgr.Len(), "老连接的回调不得删掉新登记的条目")
	testutil.True(t, mgr.GetClient(id) == replacement, "登记表里应仍是新客户端")
}

// 真实服务返回的数据必须包装成 ConnectData(connectId, data) 送回控制通道；
// 控制通道写失败时必须返回 error（此前被完全吞掉，用户数据静默丢失）。
func TestInClient_OnMessage_PropagatesSendFailure(t *testing.T) {
	testutil.SilentLog(t)

	h, _ := newHandler(netSession.SessionID(7))

	err := h.OnMessage(0, 0, []byte("user data"))
	testutil.Error(t, err,
		"M-18 未修复：控制通道写失败必须回传 error，以便引擎关闭本条转发并补发 ConnectDelete")
}

// OnConnect / OnReady 是空实现，必须可安全调用。
func TestInClient_EmptyCallbacksAreSafe(t *testing.T) {
	testutil.SilentLog(t)
	h, _ := newHandler(1)
	h.OnConnect()
	h.OnReady()
}

// 载荷包装不得就地改写调用方（真实服务）的读缓冲。
func TestInClient_OnMessage_DoesNotMutateInput(t *testing.T) {
	testutil.SilentLog(t)
	h, _ := newHandler(9)

	input := append([]byte("abcd"), make([]byte, 20)...)
	before := append([]byte(nil), input...)
	_ = h.OnMessage(0, 0, input)
	testutil.BytesEqual(t, before, input, "入参载荷被就地改写")
}

// openWithRetry 反复尝试登记一个转发客户端。
// 全套测试并发跑时真实拨号可能偶发失败（端口资源/调度），不加重试会随机红。
func openWithRetry(t *testing.T, mgr *ClientMgr, id netSession.SessionID, host string, port uint16,
	outcli netClient.NetClient) (netClient.NetClient, error) {
	t.Helper()
	for i := 0; i < 10; i++ {
		cli, err := mgr.OpenClient(id, host, port, outcli)
		if err == nil {
			return cli, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("OpenClient 连续失败")
}

// OpenClient 登记成功后，CloseClient 应能把它彻底回收。
func TestClientMgr_OpenThenCloseRoundTrip(t *testing.T) {
	testutil.SilentLog(t)

	ln, err := listenStub(t)
	testutil.NoError(t, err)
	defer ln.Close()

	mgr := NewClientMgr()
	addr := ln.Addr().(*net.TCPAddr)
	id := netSession.SessionID(555)

	cli, err := openWithRetry(t, mgr, id, "127.0.0.1", uint16(addr.Port), netClient.NewNetClient())
	testutil.NoError(t, err, "OpenClient 应成功")
	testutil.True(t, cli != nil)
	testutil.Equal(t, 1, mgr.Len())
	testutil.True(t, mgr.GetClient(id) == cli, "GetClient 应取回同一个实例")

	mgr.CloseClient(id)
	testutil.Equal(t, 0, mgr.Len())
	testutil.True(t, mgr.GetClient(id) == nil)

	// 关闭不存在的条目必须安全（乱序/重复 ConnectDelete 是常态）
	mgr.CloseClient(id)
	mgr.CloseClient(netSession.SessionID(9999))
}

// OpenClient 失败时不得留下残登记条目。
func TestClientMgr_OpenFailureLeavesNoEntry(t *testing.T) {
	testutil.SilentLog(t)

	mgr := NewClientMgr()
	// 端口 1 基本不可能有服务在听
	_, err := mgr.OpenClient(netSession.SessionID(1), "127.0.0.1", 1, netClient.NewNetClient())
	testutil.Error(t, err, "连接不存在的目标应报错")
	testutil.Equal(t, 0, mgr.Len(), "失败的 OpenClient 不应留下条目")
}

// listenStub 起一个"accept 后保持连接"的本地服务，充当内网真实服务。
func listenStub(t *testing.T) (net.Listener, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(io.Discard, c)
			}(c)
		}
	}()
	return ln, nil
}

// RemoveClient 对不存在的 id 必须安全。
func TestClientMgr_RemoveUnknownIsSafe(t *testing.T) {
	testutil.SilentLog(t)
	mgr := NewClientMgr()
	mgr.RemoveClient(netSession.SessionID(42), netClient.NewNetClient())
	testutil.Equal(t, 0, mgr.Len())
}
