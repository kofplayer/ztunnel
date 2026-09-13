package netSession

import (
	"errors"
	"sync"

	netConnect "ztunnel/engine/net/connect"
)

type SessionID uint32

const SessionIDSize = 4

type SendMessageFunc func(cb uint32, msgID uint32, data []byte) error

type NetSession interface {
	GetID() SessionID
	GetConn() netConnect.Conn
	SetConn(conn netConnect.Conn)
	SetSendMessageFunc(sendMessageFunc SendMessageFunc)
	SendMessage(cb uint32, msgID uint32, data []byte) error
	GetBindObject() interface{}
	SetBindObject(interface{})
	Close() error
}

type netSession struct {
	id SessionID

	// mu 保护下面三个跨 goroutine 读写的字段。
	//
	// 三个字段都会被多条连接的多条 goroutine 触碰：例如 inserver 处理
	// ConnectDelete 时，是在**控制通道**的 receiver goroutine 上对**用户会话**
	// 调 Close()（写 conn），而该用户连接自己的 receiver goroutine 同时在读它。
	// 此前这里一个锁都没有（报告 CRASH-01）。
	//
	// 持锁纪律：Close() 只取引用、立即解锁，**不得**持锁跨过 Disconnect()——
	// 那会在锁内执行整条 OnDisconnect 业务回调链。
	mu              sync.RWMutex
	bindObject      interface{}
	conn            netConnect.Conn
	sendMessageFunc SendMessageFunc
}

func (ns *netSession) GetBindObject() interface{} {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.bindObject
}

func (ns *netSession) SetBindObject(bindObject interface{}) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.bindObject = bindObject
}

func (ns *netSession) Init() error {
	return nil
}

func (ns *netSession) GetID() SessionID {
	return ns.id
}

func (ns *netSession) GetConn() netConnect.Conn {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.conn
}

func (ns *netSession) SetConn(conn netConnect.Conn) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.conn = conn
}

func (ns *netSession) SetSendMessageFunc(sendMessageFunc SendMessageFunc) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.sendMessageFunc = sendMessageFunc
}

func (ns *netSession) SendMessage(cb uint32, msgID uint32, data []byte) error {
	ns.mu.RLock()
	f := ns.sendMessageFunc
	ns.mu.RUnlock()
	// 会话注册进 sessionMgr 与调用方 SetSendMessageFunc 之间存在窗口，
	// 未初始化时必须报错而非 nil 函数调用 panic。
	if f == nil {
		return errors.New("send message func is not set")
	}
	return f(cb, msgID, data)
}

// Close 关闭会话对应的连接。断开通知由 ConnSocket 保证恰好一次，
// 因此业务清理（含 RemoveSession）在主动关闭路径上也会执行（报告 SEC-01）。
//
// 这里**不再**把 conn 置为 nil：OnDisconnect 回调仍需用它取 RemoteAddr 记日志，
// 置 nil 会让"先 Close 再派发 OnDisconnect"这条路必然 nil panic。
// net.Conn 在 Close 之后调用 RemoteAddr 是安全的（返回已缓存地址），
// 幂等性由 ConnSocket.disconnectOnce 负责。
func (ns *netSession) Close() error {
	ns.mu.RLock()
	conn := ns.conn
	ns.mu.RUnlock()
	if conn == nil {
		return nil
	}
	return conn.Disconnect()
}
