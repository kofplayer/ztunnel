package packageLen4Data

import (
	"encoding/binary"
	netMiddleware "ztunnel/engine/net/middleware"
)

func NewMiddleware() netMiddleware.Middleware {
	return &Len4Data{}
}

type Len4Data struct {
	netMiddleware.MiddlewareBase
	data []byte
}

func (m *Len4Data) ReceiveData(data []byte) error {
	if m.data == nil {
		m.data = data
	} else {
		m.data = append(m.data, data...)
	}
	for {
		l := uint32(len(m.data))
		if l < 4 {
			return nil
		}
		dataLen := uint32(binary.BigEndian.Uint32(m.data[0:4]))
		msgLen := dataLen + 4
		if l < msgLen {
			return nil
		}
		err := m.Next().ReceiveData(m.data[4:msgLen])
		m.data = m.data[msgLen:]
		if err != nil {
			return err
		}
	}
}

func (m *Len4Data) SendData(data []byte) error {
	sendData := make([]byte, 4, 4+len(data))
	binary.BigEndian.PutUint32(sendData[:4], uint32(len(data)))
	sendData = append(sendData, data...)
	return m.Pre().SendData(sendData)
}
