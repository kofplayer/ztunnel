package outserver

import (
	"errors"
	"testing"

	"ztunnel/common/proto"
	netConnect "ztunnel/engine/net/connect"
	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

// ctrlSession 是控制通道会话桩：可注入 SendMessage 失败、记录发出的每一帧。
type ctrlSession struct {
	id      netSession.SessionID
	sendErr error
	frames  []sentFrame
}

type sentFrame struct {
	msgID uint32
	data  []byte
}

func (c *ctrlSession) GetID() netSession.SessionID                   { return c.id }
func (c *ctrlSession) GetConn() netConnect.Conn                      { return nil }
func (c *ctrlSession) SetConn(netConnect.Conn)                       {}
func (c *ctrlSession) Init() error                                   { return nil }
func (c *ctrlSession) GetBindObject() any                            { return nil }
func (c *ctrlSession) SetBindObject(any)                             {}
func (c *ctrlSession) SetSendMessageFunc(netSession.SendMessageFunc) {}
func (c *ctrlSession) Close() error                                  { return nil }
func (c *ctrlSession) SendMessage(cb uint32, msgID uint32, data []byte) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	c.frames = append(c.frames, sentFrame{msgID, append([]byte(nil), data...)})
	return nil
}
func (c *ctrlSession) count(msgID uint32) int {
	n := 0
	for _, f := range c.frames {
		if f.msgID == msgID {
			n++
		}
	}
	return n
}
func (c *ctrlSession) last(msgID uint32) []byte {
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].msgID == msgID {
			return c.frames[i].data
		}
	}
	return nil
}

// userSession 是终端用户侧会话桩，统计被要求关闭的次数。
type userSession struct {
	netSession.NetSession
	id     netSession.SessionID
	closes int
}

func (u *userSession) GetID() netSession.SessionID { return u.id }
func (u *userSession) Close() error                { u.closes++; return nil }

// ---------------------------------------------------------------------------

// 终端用户接入完成时必须经控制通道上报 ConnectNew(connectId)。
func TestOutServer_OnReady_ReportsConnectNew(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1}
	h := &handler{inServerSession: ctrl}
	user := &userSession{id: 55}

	h.OnReady(user)
	testutil.Equal(t, 1, ctrl.count(proto.MsgIdConnectNew), "应上报一条 ConnectNew")

	body := ctrl.last(proto.MsgIdConnectNew)
	testutil.Equal(t, netSession.SessionIDSize, len(body), "ConnectNew 载荷应恰好是 4 字节 connectId")
	testutil.Equal(t, user.id, proto.ReadSessionId(body), "上报的 connectId 必须是该会话自己的 ID")
}

// 控制通道写失败 = 隧道已死。必须关掉这条用户会话，否则终端用户会挂在
// 一个永远等不到回应的连接上（此前返回值被完全忽略）。
func TestOutServer_OnReady_SendFailureClosesUserSession(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1, sendErr: errors.New("control channel gone")}
	h := &handler{inServerSession: ctrl}
	user := &userSession{id: 56}

	h.OnReady(user)
	testutil.Equal(t, 1, user.closes,
		"上报失败后必须关闭用户会话，不能让它挂在无回应的连接上")
}

// 用户断开时必须通知 client 回收对应转发。
func TestOutServer_OnDisconnect_ReportsConnectDelete(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1}
	h := &handler{inServerSession: ctrl}
	user := &userSession{id: 77}

	h.OnDisconnect(user)
	testutil.Equal(t, 1, ctrl.count(proto.MsgIdConnectDelete), "应上报一条 ConnectDelete")
	testutil.Equal(t, user.id, proto.ReadSessionId(ctrl.last(proto.MsgIdConnectDelete)))
}

// OnDisconnect 没有返回值可用，失败只能记日志——但绝不能 panic。
func TestOutServer_OnDisconnect_SendFailureDoesNotPanic(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1, sendErr: errors.New("control channel gone")}
	h := &handler{inServerSession: ctrl}

	h.OnDisconnect(&userSession{id: 78}) // 不得 panic
}

// 用户数据必须包装成 ConnectData(connectId, data) 送进控制通道。
func TestOutServer_OnMessage_WrapsWithConnectId(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1}
	h := &handler{inServerSession: ctrl}
	user := &userSession{id: 4}

	payload := []byte("q1w2e3")
	testutil.NoError(t, h.OnMessage(user, 0, 0, payload))

	got := ctrl.last(proto.MsgIdConnectData)
	want := append([]byte{0, 0, 0, 4}, payload...)
	testutil.BytesEqual(t, want, got, "ConnectData 应为 4 字节大端 connectId + 原始载荷")
}

// 载荷与头部必须分离：包装不得就地改写调用方的读缓冲。
func TestOutServer_OnMessage_DoesNotMutateCallerPayload(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1}
	h := &handler{inServerSession: ctrl}

	payload := append([]byte("abc"), make([]byte, 40)...) // cap 明显大于 len
	before := append([]byte(nil), payload...)
	_ = h.OnMessage(&userSession{id: 9}, 0, 0, payload)

	testutil.Equal(t, len(before), len(payload), "调用方切片长度被改动")
	testutil.BytesEqual(t, before, payload, "调用方载荷被就地改写")
}

// 送不进控制通道时返回 error，让引擎关闭这条用户连接——继续收数据只会把
// 用户数据静默倒进黑洞（报告 M-18）。
func TestOutServer_OnMessage_SendFailureReturnsError(t *testing.T) {
	testutil.SilentLog(t)

	ctrl := &ctrlSession{id: 1, sendErr: errors.New("control channel gone")}
	h := &handler{inServerSession: ctrl}

	testutil.Error(t, h.OnMessage(&userSession{id: 2}, 0, 0, []byte("xy")),
		"转发失败必须回传 error，以便关闭这条用户连接")
}

// OnConnect 是空实现，必须可安全调用。
func TestOutServer_OnConnectIsSafeNoop(t *testing.T) {
	testutil.SilentLog(t)
	h := &handler{inServerSession: &ctrlSession{id: 1}}
	h.OnConnect(&userSession{id: 3})
}
