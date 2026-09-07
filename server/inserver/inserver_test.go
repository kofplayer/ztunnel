package inserver

import (
	"encoding/binary"
	"net"
	"strconv"
	"testing"

	"ztunnel/common/proto"
	netServer "ztunnel/engine/net/server"
	"ztunnel/testutil"
)

const testToken = "tok123"

func newHandlerSession() (*handler, *testutil.FakeSession) {
	return &handler{}, testutil.NewFakeSession(1)
}

func createTunnelData(token string, port uint16) []byte {
	data := append([]byte(token), 0, 0)
	binary.BigEndian.PutUint16(data[len(token):], port)
	return data
}

func stopBindObject(t *testing.T, s *testutil.FakeSession) {
	t.Helper()
	if bo := s.GetBindObject(); bo != nil {
		_ = bo.(netServer.NetServer).Stop()
	}
}

// ---- 基线行为固化 ----

func TestInServer_CreateTunnel_TokenCheck(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken(testToken)
	t.Cleanup(func() { proto.SetToken("") })

	h, s := newHandlerSession()
	port := uint16(testutil.FreePort(t))

	// 正确 token：建隧道成功
	testutil.NoError(t, h.OnMessage(s, 0, proto.MsgIdCreateTunnel, createTunnelData(testToken, port)))
	resp := s.LastSent(proto.MsgIdCreateTunnel)
	testutil.True(t, len(resp) == 1 && resp[0] == proto.ErrorCodeNone,
		"成功建隧道应答应为 ErrorCodeNone, got", resp)
	testutil.True(t, s.GetBindObject() != nil, "成功后应绑定 outserver")
	stopBindObject(t, s)

	// 重复创建应被拒绝
	err := h.OnMessage(s, 0, proto.MsgIdCreateTunnel, createTunnelData(testToken, port))
	testutil.Error(t, err, "重复 CreateTunnel 应报错")
}

func TestInServer_CreateTunnel_BadToken(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken(testToken)
	t.Cleanup(func() { proto.SetToken("") })

	h, s := newHandlerSession()
	err := h.OnMessage(s, 0, proto.MsgIdCreateTunnel, createTunnelData("wrong", 3333))
	testutil.Error(t, err, "错误 token 应拒绝")
	testutil.True(t, s.GetBindObject() == nil, "拒绝后不应绑定 outserver")
	testutil.Equal(t, 0, s.SentCount(proto.MsgIdCreateTunnel), "拒绝后不应有成功应答")
}

func TestInServer_CreateTunnel_BadLength(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken(testToken)
	t.Cleanup(func() { proto.SetToken("") })

	h, s := newHandlerSession()
	err := h.OnMessage(s, 0, proto.MsgIdCreateTunnel, []byte("short"))
	testutil.Error(t, err, "长度不符应拒绝")
}

// ---- 回归用例（修复前红） ----

// 回归 #2：未建隧道（BindObject 为 nil）时发送 ConnectDelete，
// s.GetBindObject().(netServer.NetServer) 对 nil 接口断言 → panic。
// 任意未认证 TCP 连接无需 token 即可触发，进程崩溃。
// 修复后：应返回错误（连接被关闭），不得 panic。
func TestInServer_ConnectDelete_BeforeTunnel_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h, s := newHandlerSession()
		_ = h.OnMessage(s, 0, proto.MsgIdConnectDelete, []byte{0, 0, 0, 1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #2 未修复：未建隧道即发 ConnectDelete 触发 panic（未认证远程崩溃）:\n%v", err)
	}
}

// 回归 #2 同源：未建隧道时发送失败码 ConnectNew，同样命中 nil 断言 panic。
func TestInServer_ConnectNewError_BeforeTunnel_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h, s := newHandlerSession()
		_ = h.OnMessage(s, 0, proto.MsgIdConnectNew, []byte{proto.ErrorCodeNormal, 0, 0, 0, 1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #2 未修复：未建隧道即发失败码 ConnectNew 触发 panic:\n%v", err)
	}
}

// 回归 #8：export 端口被占用时，outserver 异步启动、失败被静默吞掉，
// 但成功应答已先行发出 → 客户端收到"假成功"。修复（同步 listen 后再应答）前：
// 应答为成功码（红）；修复后：应答必须是错误码。
func TestInServer_CreateTunnel_PortInUse_ReportsError(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken(testToken)
	t.Cleanup(func() { proto.SetToken("") })

	port := testutil.FreePort(t)
	// 注意必须绑通配地址：Windows 下先绑具体 IP 不影响后续通配绑定，会绕过冲突
	occupier, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(port))
	testutil.NoError(t, err)
	defer occupier.Close()

	h, s := newHandlerSession()
	testutil.NoError(t, h.OnMessage(s, 0, proto.MsgIdCreateTunnel, createTunnelData(testToken, uint16(port))))
	resp := s.LastSent(proto.MsgIdCreateTunnel)
	testutil.True(t, len(resp) == 1 && resp[0] != proto.ErrorCodeNone,
		"回归 #8 未修复：export 端口被占时应答必须是错误码，当前返回", resp)
	stopBindObject(t, s)
}
