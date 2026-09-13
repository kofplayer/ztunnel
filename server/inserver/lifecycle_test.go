package inserver

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"ztunnel/common/proto"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// ctrlFrame 按控制通道的真实线格式组帧：len(4B BE) | msgId(1B) | data
func ctrlFrame(msgID byte, payload []byte) []byte {
	body := append([]byte{msgID}, payload...)
	out := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	return append(out, body...)
}

func readFull(c net.Conn, b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := c.Read(b[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func readCtrlFrame(c net.Conn) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := readFull(c, h); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint32(h))
	_, err := readFull(c, b)
	return b, err
}

// 回归 SEC-01（端到端，预认证攻击路径）：
//
// 建隧道成功后再发一条畸形帧 → handler 返回 error → common/server 的包装层
// 先 s.Close()（关掉发送队列）→ 错误回到 receiverRun 时被 `if !q.IsClose()`
// 守卫拦掉 → inserver.OnDisconnect 永不执行 → outServer.Stop() 永不执行。
//
// 后果有两个，都必须被断言修掉：
//  1. 控制会话永久留在 inserver 的 sessionMgr 里（其 bindObject 钉住整个
//     outserver 对象图）→ 攻击者反复连接即可无界消耗内存；
//  2. export 端口的 listener 变成僵尸且永不释放 → 之后任何合法客户端重连
//     该端口都是 EADDRINUSE，**只能重启服务端恢复**；期间终端用户仍可连入，
//     但控制通道已死，连接全部挂起。
func TestInServer_ControlSessionAndPortAreReclaimed(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("probe")
	t.Cleanup(func() { proto.SetToken("") })
	proto.NetEncrypt = false

	ctrlPort := testutil.FreePort(t)
	exportPort := uint16(testutil.FreePort(t))

	svr := NewServer("", uint16(ctrlPort), "")
	go func() { _ = svr.Start() }()
	t.Cleanup(func() { _ = svr.Stop() })

	addr := "127.0.0.1:" + strconv.Itoa(ctrlPort)
	var conn net.Conn
	if !testutil.Eventually(t, 3*time.Second, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		conn = c
		return true
	}) {
		t.Fatal("服务端未就绪")
	}
	defer conn.Close()

	tunnelReq := func() []byte {
		p := append([]byte("probe"), 0, 0)
		binary.BigEndian.PutUint16(p[len("probe"):], exportPort)
		return ctrlFrame(proto.MsgIdCreateTunnel, p)
	}

	// 1) 正常建隧道
	_, err := conn.Write(tunnelReq())
	testutil.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := readCtrlFrame(conn)
	testutil.NoError(t, err, "应收到 CreateTunnel 应答")
	testutil.True(t, len(resp) == 2 && resp[1] == proto.ErrorCodeNone,
		"建隧道应成功，实际应答", resp)

	// 2) 重复 CreateTunnel → handler 返回 error → s.Close()
	if _, err := conn.Write(tunnelReq()); err != nil {
		// 连接可能已被服务端抢先关闭，不影响断言
		t.Logf("第二帧写入失败（连接已被服务端关闭，正常）: %v", err)
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := readCtrlFrame(conn); err != nil {
			break
		}
	}
	_ = conn.Close()

	// 3) 断言 A：控制会话必须被回收
	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return svr.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("SEC-01 未修复：控制会话仍留在 sessionMgr（Len=%d），期望 0",
			svr.GetSessionMgr().Len()))

	// 4) 断言 B：export 端口必须被释放（outServer.Stop() 真的执行了）
	released := testutil.Eventually(t, 3*time.Second, func() bool {
		l, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(int(exportPort)))
		if err != nil {
			return false
		}
		_ = l.Close()
		return true
	})
	testutil.True(t, released,
		fmt.Sprintf("SEC-01 未修复：export 端口 %d 仍被僵尸 listener 占用，outserver.Stop() 从未执行；"+
			"合法客户端将永远无法在该端口重建隧道", exportPort))
}

// 回归 SEC-01 的正常业务路径：client 上报 ConnectDelete 后，outserver 侧的
// 用户会话必须被真正回收。此前 inserver.go 只做 session.Close()，
// RemoveSession 被同一个守卫吞掉 → 内网服务每先关一次连接（MySQL
// wait_timeout、HTTP keep-alive 超时、Redis 空闲断开）就永久残留一个条目。
func TestInServer_ConnectDelete_ReclaimsUserSession(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("probe")
	t.Cleanup(func() { proto.SetToken("") })
	proto.NetEncrypt = false

	ctrlPort := testutil.FreePort(t)
	exportPort := uint16(testutil.FreePort(t))

	svr := NewServer("", uint16(ctrlPort), "")
	go func() { _ = svr.Start() }()
	t.Cleanup(func() { _ = svr.Stop() })

	conn := dial(t, "127.0.0.1:"+strconv.Itoa(ctrlPort))
	defer conn.Close()

	p := append([]byte("probe"), 0, 0)
	binary.BigEndian.PutUint16(p[len("probe"):], exportPort)
	_, err := conn.Write(ctrlFrame(proto.MsgIdCreateTunnel, p))
	testutil.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := readCtrlFrame(conn)
	testutil.NoError(t, err)
	testutil.True(t, len(resp) == 2 && resp[1] == proto.ErrorCodeNone, "建隧道应成功", resp)

	// 取到本控制会话绑定的 outserver
	bind := firstBoundOutServer(t, svr)
	testutil.True(t, bind != nil, "未找到绑定了 outserver 的控制会话")

	// 终端用户连入 export 端口 → outserver 建会话并经控制通道上报 ConnectNew
	user := dial(t, "127.0.0.1:"+strconv.Itoa(int(exportPort)))
	defer user.Close()

	if !testutil.Eventually(t, 3*time.Second, func() bool {
		return bind.GetSessionMgr().Len() == 1
	}) {
		t.Fatalf("用户会话未注册，Len=%d", bind.GetSessionMgr().Len())
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	newMsg, err := readCtrlFrame(conn)
	testutil.NoError(t, err, "应收到 outserver 上报的 ConnectNew")
	testutil.True(t, len(newMsg) == 1+netSession.SessionIDSize && newMsg[0] == proto.MsgIdConnectNew,
		"ConnectNew 帧格式不符", newMsg)
	connectId := newMsg[1:]

	// 控制端回 ConnectDelete(connectId)
	_, err = conn.Write(ctrlFrame(proto.MsgIdConnectDelete, connectId))
	testutil.NoError(t, err)

	ok := testutil.Eventually(t, 3*time.Second, func() bool {
		return bind.GetSessionMgr().Len() == 0
	})
	testutil.True(t, ok,
		fmt.Sprintf("SEC-01 未修复：收到 ConnectDelete 后 outserver 会话仍在 map 中（Len=%d）",
			bind.GetSessionMgr().Len()))
}

// firstBoundOutServer 扫描 inserver 的会话表，返回第一个绑定了 outserver 的会话所绑的对象。
func firstBoundOutServer(t *testing.T, svr netServer.NetServer) netServer.NetServer {
	t.Helper()
	var found netServer.NetServer
	svr.GetSessionMgr().TravelSession(func(s netSession.NetSession) bool {
		if bo := s.GetBindObject(); bo != nil {
			if out, ok := bo.(netServer.NetServer); ok {
				found = out
				return false
			}
		}
		return true
	})
	return found
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	var c net.Conn
	if !testutil.Eventually(t, 3*time.Second, func() bool {
		cc, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		c = cc
		return true
	}) {
		t.Fatalf("无法连接 %s", addr)
	}
	return c
}
