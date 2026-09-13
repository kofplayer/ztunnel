package inserver

import (
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"ztunnel/common/proto"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

func frame(msgID byte, payload []byte) []byte {
	body := append([]byte{msgID}, payload...)
	out := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	return append(out, body...)
}

func readFrame(c net.Conn) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := readFull(c, h); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint32(h))
	_, err := readFull(c, b)
	return b, err
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

func countSessions(svr interface {
	GetSessionMgr() netSession.SessionMgr
}) int {
	n := 0
	svr.GetSessionMgr().TravelSession(func(netSession.NetSession) bool {
		n++
		return true
	})
	return n
}

// 探针：坏 token 之后再来一条畸形帧，控制会话与其绑定的 outserver 是否被回收。
func TestProbe_ControlSessionLeaks(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("probe")
	t.Cleanup(func() { proto.SetToken("") })
	proto.NetEncrypt = false

	ctrlPort := testutil.FreePort(t)
	exportPort := uint16(testutil.FreePort(t))

	svr := NewServer("", uint16(ctrlPort))
	go func() { _ = svr.Start() }()

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
		return frame(proto.MsgIdCreateTunnel, p)
	}

	if _, err := conn.Write(tunnelReq()); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	resp, err := readFrame(conn)
	if err != nil {
		t.Fatalf("读应答失败: %v", err)
	}
	if len(resp) != 2 || resp[1] != proto.ErrorCodeNone {
		t.Fatalf("建隧道应成功, got %v", resp)
	}
	t.Logf("隧道已建立，export port=%d", exportPort)

	// 第二条重复 CreateTunnel → handler 返回 error → 工厂 s.Close()
	if _, err := conn.Write(tunnelReq()); err != nil {
		t.Logf("第二帧写失败（连接已被服务端关闭，正常）: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, err := readFrame(conn); err != nil {
			break
		}
	}
	conn.Close()

	closed := testutil.Eventually(t, 3*time.Second, func() bool {
		return countSessions(svr) == 0
	})
	if !closed {
		t.Errorf("泄漏：控制会话仍留在 sessionMgr 中，数量=%d（期望 0）", countSessions(svr))
	}

	occupied := testutil.Eventually(t, 3*time.Second, func() bool {
		l, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(int(exportPort)))
		if err != nil {
			return false
		}
		_ = l.Close()
		return true
	})
	if !occupied {
		t.Errorf("僵尸 listener：export port %d 仍被占用，outserver.Stop() 从未执行", exportPort)
	}
}
