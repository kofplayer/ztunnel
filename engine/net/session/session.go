package netSession

import (
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
	id              SessionID
	bindObject      interface{}
	conn            netConnect.Conn
	sendMessageFunc SendMessageFunc
}

func (ns *netSession) GetBindObject() interface{} {
	return ns.bindObject
}

func (ns *netSession) SetBindObject(bindObject interface{}) {
	ns.bindObject = bindObject
}

func (ns *netSession) Init() error {
	return nil
}

func (ns *netSession) GetID() SessionID {
	return ns.id
}

func (ns *netSession) GetConn() netConnect.Conn {
	return ns.conn
}

func (ns *netSession) SetConn(conn netConnect.Conn) {
	ns.conn = conn
}

func (ns *netSession) SetSendMessageFunc(sendMessageFunc SendMessageFunc) {
	ns.sendMessageFunc = sendMessageFunc
}

func (ns *netSession) SendMessage(cb uint32, msgID uint32, data []byte) error {
	return ns.sendMessageFunc(cb, msgID, data)
}

func (ns *netSession) Close() error {
	if ns.conn != nil {
		err := ns.conn.Disconnect()
		ns.conn = nil
		return err
	}
	return nil
}
