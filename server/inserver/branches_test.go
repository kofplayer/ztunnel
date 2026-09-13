package inserver

import (
	"errors"
	"testing"

	"ztunnel/common/proto"
	netConnect "ztunnel/engine/net/connect"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// ---------------------------------------------------------------------------
// 替身
// ---------------------------------------------------------------------------

// userSession 是可控的用户会话桩：能注入 SendMessage 失败、并统计 Close 次数。
type userSession struct {
	id        netSession.SessionID
	sendErr   error
	closes    int
	sent      [][]byte
	conn      netConnect.Conn
	bindValue any
}

func (u *userSession) GetID() netSession.SessionID                   { return u.id }
func (u *userSession) GetConn() netConnect.Conn                      { return u.conn }
func (u *userSession) SetConn(c netConnect.Conn)                     { u.conn = c }
func (u *userSession) Init() error                                   { return nil }
func (u *userSession) GetBindObject() any                            { return u.bindValue }
func (u *userSession) SetBindObject(v any)                           { u.bindValue = v }
func (u *userSession) SetSendMessageFunc(netSession.SendMessageFunc) {}
func (u *userSession) Close() error {
	u.closes++
	return nil
}
func (u *userSession) SendMessage(cb uint32, msgID uint32, data []byte) error {
	if u.sendErr != nil {
		return u.sendErr
	}
	u.sent = append(u.sent, append([]byte(nil), data...))
	return nil
}

// stubMgr 只提供 inserver 真正会用到的三个方法。
type stubMgr struct {
	sessions map[netSession.SessionID]*userSession
}

func (m *stubMgr) NewSession() netSession.NetSession { return nil }
func (m *stubMgr) RemoveSession(id netSession.SessionID) {
	delete(m.sessions, id)
}
func (m *stubMgr) GetSession(id netSession.SessionID) netSession.NetSession {
	if s, ok := m.sessions[id]; ok {
		return s
	}
	return nil
}
func (m *stubMgr) TravelSession(func(netSession.NetSession) bool) {}
func (m *stubMgr) Len() int                                       { return len(m.sessions) }

// stubOutServer 满足 bindOutServer 的类型断言，并持有可控的会话表。
type stubOutServer struct {
	netServer.NetServer // 借方法签名；被调用到未覆写的方法即 nil panic，正是我们要的信号
	mgr                 *stubMgr
	stopped             int
}

func (s *stubOutServer) GetSessionMgr() netSession.SessionMgr { return s.mgr }
func (s *stubOutServer) Stop() error {
	s.stopped++
	return nil
}

func sessionBytes(id netSession.SessionID) []byte {
	b := make([]byte, netSession.SessionIDSize)
	proto.WriteSessionId(b, id)
	return b
}

// newBoundSetup 构造"控制会话已绑定 outserver、其下有一条 id=77 的用户会话"。
func newBoundSetup(t *testing.T) (*handler, *testutil.FakeSession, *stubOutServer, *userSession) {
	t.Helper()
	h := &handler{}
	ctrl := testutil.NewFakeSession(1)

	user := &userSession{id: 77}
	out := &stubOutServer{mgr: &stubMgr{sessions: map[netSession.SessionID]*userSession{77: user}}}
	ctrl.SetBindObject(out)
	return h, ctrl, out, user
}

// ---------------------------------------------------------------------------
// ConnectDelete / ConnectNew：必须真正关掉对应用户会话
// ---------------------------------------------------------------------------

// 回归 SEC-01 的正常业务路径：内网服务先关（MySQL wait_timeout、HTTP keep-alive
// 超时、Redis 空闲断开）时 client 上报 ConnectDelete，服务端必须关掉 outserver 里
// 那个用户会话；否则终端用户的 TCP 会一直挂着，且会话条目永久残留在 map 中。
func TestInServer_ConnectDelete_ClosesUserSession(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)

	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectDelete, sessionBytes(user.id)))
	testutil.Equal(t, 1, user.closes, "收到 ConnectDelete 后应关闭对应用户会话")
}

// 内网服务连不上时 client 上报 code≠0，服务端同样必须关掉该用户会话。
func TestInServer_ConnectNewError_ClosesUserSession(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)

	data := append([]byte{proto.ErrorCodeNormal}, sessionBytes(user.id)...)
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectNew, data))
	testutil.Equal(t, 1, user.closes, "连接失败码应导致关闭用户会话")
}

// code==0 只代表转发链路就绪，绝不能误关会话。
func TestInServer_ConnectNewSuccess_KeepsUserSession(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)

	data := append([]byte{proto.ErrorCodeNone}, sessionBytes(user.id)...)
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectNew, data))
	testutil.Equal(t, 0, user.closes, "成功码不应关闭用户会话")
}

// 未知 connectId 必须安静忽略（乱序/重复删除是常态），而不是 panic。
func TestInServer_UnknownConnectId_IsIgnored(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, _ := newBoundSetup(t)

	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectDelete, sessionBytes(4242)))
}

// ---------------------------------------------------------------------------
// ConnectData 转发
// ---------------------------------------------------------------------------

// 用户数据必须剥掉 4 字节 connectId 后**原样**送到对应会话。
func TestInServer_ConnectData_ForwardsStrippedPayload(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)

	body := append(sessionBytes(user.id), 'h', 'i')
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectData, body))

	testutil.Equal(t, 1, len(user.sent), "应转发一帧")
	testutil.BytesEqual(t, []byte{'h', 'i'}, user.sent[0], "转发内容应为剥掉头部后的载荷")
}

// 转发失败说明这条用户连接已废：必须关掉它，而不是静默把后续数据倒进黑洞。
func TestInServer_ConnectData_SendFailureClosesSession(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)
	user.sendErr = errors.New("user conn gone")

	body := append(sessionBytes(user.id), 'a')
	// 注意：不能 return error —— 那会让工厂层关掉**控制通道**，
	// 一个用户的失败不该拆掉整条隧道。
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectData, body))
	testutil.Equal(t, 1, user.closes, "转发失败后应只关闭该用户会话")
}

// 找不到用户会话时必须跳过而不是 panic。
func TestInServer_ConnectData_UnknownSessionIsSkipped(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, _ := newBoundSetup(t)

	body := append(sessionBytes(999), 'x')
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdConnectData, body))
}

// ---------------------------------------------------------------------------
// 畸形帧
// ---------------------------------------------------------------------------

func TestInServer_MalformedFrames_ReturnError(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, _, user := newBoundSetup(t)

	cases := []struct {
		name  string
		msgID uint32
		data  []byte
	}{
		{"ConnectNew 长度不足", proto.MsgIdConnectNew, []byte{0, 1, 2}},
		{"ConnectNew 长度超长", proto.MsgIdConnectNew, make([]byte, netSession.SessionIDSize+2)},
		{"ConnectDelete 长度不足", proto.MsgIdConnectDelete, []byte{1, 2}},
		{"ConnectDelete 长度超长", proto.MsgIdConnectDelete, make([]byte, netSession.SessionIDSize+1)},
		{"ConnectData 不足一个头部", proto.MsgIdConnectData, []byte{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Error(t, h.OnMessage(ctrl, 0, tc.msgID, tc.data), "畸形帧必须返回 error")
		})
	}

	// 未知 msgId 应被忽略而非致命
	testutil.NoError(t, h.OnMessage(ctrl, 0, 99, []byte{1}), "未知 msgId 应被忽略")
	_ = user
}

// ---------------------------------------------------------------------------
// 未绑定（未认证）会话：一律拒绝，绝不 panic（报告 #2 的回归面）
// ---------------------------------------------------------------------------

func TestInServer_UnboundSession_RejectsMessages(t *testing.T) {
	testutil.SilentLog(t)
	h := &handler{}
	ctrl := testutil.NewFakeSession(5) // 未 SetBindObject

	// 长度合法的 ConnectNew(code≠0) / ConnectDelete / ConnectData
	cases := []struct {
		name  string
		msgID uint32
		data  []byte
	}{
		{"ConnectNew", proto.MsgIdConnectNew, append([]byte{proto.ErrorCodeNormal}, sessionBytes(7)...)},
		{"ConnectDelete", proto.MsgIdConnectDelete, sessionBytes(7)},
		{"ConnectData", proto.MsgIdConnectData, append(sessionBytes(7), 'z')},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := h.OnMessage(ctrl, 0, tc.msgID, tc.data)
			testutil.Error(t, err, "未绑定会话必须拒绝消息而非 nil 断言 panic")
		})
	}
}

// ---------------------------------------------------------------------------
// OnDisconnect / 空回调
// ---------------------------------------------------------------------------

// 未绑定 outserver 的控制会话断开时不得 panic，也不得去 Stop 任何东西。
func TestInServer_OnDisconnect_UnboundIsSafe(t *testing.T) {
	testutil.SilentLog(t)
	h := &handler{}
	ctrl := testutil.NewFakeSession(9)

	h.OnConnect(ctrl) // 空实现，必须可安全调用
	h.OnReady(ctrl)
	h.OnDisconnect(ctrl)
}

// 已绑定隧道的控制会话断开时必须 Stop 掉 outserver（连带回收其全部用户会话）。
func TestInServer_OnDisconnect_StopsBoundOutServer(t *testing.T) {
	testutil.SilentLog(t)
	h, ctrl, out, _ := newBoundSetup(t)

	h.OnDisconnect(ctrl)
	testutil.Equal(t, 1, out.stopped, "控制通道断开应停止对应的 outserver")
}

// ---------------------------------------------------------------------------
// CreateTunnel 的成功与失败分支（补 lifecycle_test 未覆盖的路径）
// ---------------------------------------------------------------------------

func TestInServer_CreateTunnel_ZeroOutPort_RespondsError(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	h := &handler{}
	ctrl := testutil.NewFakeSession(3)

	data := append([]byte("tk"), 0, 0) // outPort = 0
	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdCreateTunnel, data),
		"outPort=0 应回错误码而不是断连")

	last := ctrl.LastSent(proto.MsgIdCreateTunnel)
	testutil.True(t, len(last) == 1 && last[0] == proto.ErrorCodeNormal,
		"outPort=0 应应答错误码, got", last)
	testutil.True(t, ctrl.GetBindObject() == nil, "被拒绝后不应绑定 outserver")
}

func TestInServer_CreateTunnel_WrongTokenLength_Rejected(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("abcdef")
	t.Cleanup(func() { proto.SetToken("") })

	h := &handler{}
	ctrl := testutil.NewFakeSession(4)

	// 长度按 TokenLen=6 算，但只给了 3 字节
	testutil.Error(t, h.OnMessage(ctrl, 0, proto.MsgIdCreateTunnel, []byte("abc")),
		"长度与 TokenLen 不符必须拒绝")
}

// 长度合法但 token 内容不符：必须拒绝（并且走的是常量时间比较）。
func TestInServer_CreateTunnel_WrongTokenValue_Rejected(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("abcdef")
	t.Cleanup(func() { proto.SetToken("") })

	h := &handler{}
	ctrl := testutil.NewFakeSession(6)

	// 6 字节 token + 2 字节端口，长度完全合法，但 token 值差一个字节
	data := append([]byte("abcdeg"), 0x0D, 0x08)
	testutil.Error(t, h.OnMessage(ctrl, 0, proto.MsgIdCreateTunnel, data),
		"token 值不符必须拒绝")
	testutil.True(t, ctrl.GetBindObject() == nil, "token 不符时不得绑定 outserver")
}

// 建隧道应答写失败时必须留痕且不得 panic（此前 8 处 SendMessage 返回值被静默吞掉）。
// 用 outPort=0 走同一条 replyCode 分支，避免真的建出一个需要清理的 outserver。
func TestInServer_CreateTunnel_ReplyFailureIsLoggedNotPanicked(t *testing.T) {
	testutil.SilentLog(t)
	proto.SetToken("tk")
	t.Cleanup(func() { proto.SetToken("") })

	h := &handler{}
	// 复用可注入发送失败的会话桩当作控制会话
	ctrl := &userSession{id: 88, sendErr: errors.New("control channel gone")}

	testutil.NoError(t, h.OnMessage(ctrl, 0, proto.MsgIdCreateTunnel, []byte("tk\x00\x00")),
		"应答写失败不应升级为致命错误")
	testutil.Equal(t, 0, len(ctrl.sent),
		"控制通道写失败时一帧都不应发出")
}
