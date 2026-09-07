package testutil

import (
	"sync"

	netConnect "ztunnel/engine/net/connect"
	netSession "ztunnel/engine/net/session"
)

// FakeConn 是 netConnect.Conn 的内存桩。
type FakeConn struct {
	mu          sync.Mutex
	sent        [][]byte
	disconnects int
}

func (c *FakeConn) RemoteAddr() string { return "203.0.113.7:44444" }

func (c *FakeConn) Disconnect() error {
	c.mu.Lock()
	c.disconnects++
	c.mu.Unlock()
	return nil
}

func (c *FakeConn) SendData(b []byte) error {
	cp := append([]byte(nil), b...)
	c.mu.Lock()
	c.sent = append(c.sent, cp)
	c.mu.Unlock()
	return nil
}

func (c *FakeConn) SetOnDisconnect(f netConnect.OnDisconnectFunc) {}

func (c *FakeConn) SetOnData(f netConnect.OnDataFunc) {}

func (c *FakeConn) DisconnectCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnects
}

// FakeSession 是 netSession.NetSession 的内存桩：可自由控制 BindObject（含 nil，
// 用于报告 #2 的未认证崩溃回归）、记录 SendMessage 调用与 Close 次数。
type FakeSession struct {
	id   netSession.SessionID
	mu   sync.Mutex
	bind any
	sent []SentMsg
	conn *FakeConn
}

type SentMsg struct {
	CB    uint32
	MsgID uint32
	Data  []byte
}

func NewFakeSession(id netSession.SessionID) *FakeSession {
	return &FakeSession{id: id, conn: &FakeConn{}}
}

func (f *FakeSession) GetID() netSession.SessionID { return f.id }

func (f *FakeSession) GetConn() netConnect.Conn { return f.conn }

func (f *FakeSession) SetConn(conn netConnect.Conn) {}

func (f *FakeSession) Init() error { return nil }

func (f *FakeSession) SetSendMessageFunc(fn netSession.SendMessageFunc) {}

func (f *FakeSession) SendMessage(cb uint32, msgID uint32, data []byte) error {
	cp := append([]byte(nil), data...)
	f.mu.Lock()
	f.sent = append(f.sent, SentMsg{CB: cb, MsgID: msgID, Data: cp})
	f.mu.Unlock()
	return nil
}

func (f *FakeSession) GetBindObject() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bind
}

func (f *FakeSession) SetBindObject(v any) {
	f.mu.Lock()
	f.bind = v
	f.mu.Unlock()
}

func (f *FakeSession) Close() error {
	if f.conn != nil {
		return f.conn.Disconnect()
	}
	return nil
}

// LastSent 返回最后一条指定 MsgID 的消息数据（无则返回 nil）。
func (f *FakeSession) LastSent(msgID uint32) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].MsgID == msgID {
			return f.sent[i].Data
		}
	}
	return nil
}

// SentCount 返回指定 MsgID 的消息条数。
func (f *FakeSession) SentCount(msgID uint32) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.sent {
		if m.MsgID == msgID {
			n++
		}
	}
	return n
}
