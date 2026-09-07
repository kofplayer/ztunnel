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

// 回归 #9：Stop 此前只关 listener，已建立的会话全部悬挂不释放。
// 修复后 Stop 必须先关闭全部存量会话（客户端能读到连接关闭），再停监听。
func TestNetServer_Stop_ClosesAllSessions(t *testing.T) {
	testutil.SilentLog(t)

	svr := NewNetServer()
	acc := socketNetConnect.NewAcceptor()
	port := testutil.FreePort(t)
	acc.SetAddress("127.0.0.1", uint16(port))
	svr.SetAcceptor(acc)
	svr.SetCodec(netCodec.NewCodec_data())
	accepted := make(chan struct{}, 1)
	svr.SetOnAccept(func(s netSession.NetSession) {
		accepted <- struct{}{}
	})
	svr.SetOnMessage(func(s netSession.NetSession, cb uint32, msgID uint32, data []byte) error {
		return nil
	})
	go func() { _ = svr.Start() }()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	var client net.Conn
	testutil.True(t, testutil.Eventually(t, 3*time.Second, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		client = conn
		return true
	}), "服务端未就绪")
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()

	// 等待服务端完成 accept、会话注册完成
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("服务端未 accept 连接")
	}

	testutil.NoError(t, svr.Stop())

	// 客户端必须能读到连接被服务端关闭（EOF/错误），而非无限悬挂
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	_, err := client.Read(buf)
	testutil.Error(t, err, "回归 #9 未修复：服务端 Stop 后存量会话未关闭（客户端仍悬挂）")
}
