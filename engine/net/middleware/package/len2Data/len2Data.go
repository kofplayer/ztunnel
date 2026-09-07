package packageLen2Data

import (
	"encoding/binary"

	netMiddleware "ztunnel/engine/net/middleware"
)

func NewMiddleware() netMiddleware.Middleware {
	return &Len2Data{}
}

type Len2Data struct {
	netMiddleware.MiddlewareBase
	data []byte
}

func (m *Len2Data) ReceiveData(data []byte) error {
	// 必须拷贝：调用方复用读取缓冲，别名会导致已转发帧被覆盖（报告 #5）。
	// uint16 长度天然上限 64KB，无回绕/无上限校验需求。
	if m.data == nil {
		m.data = make([]byte, 0, len(data)+64)
	}
	m.data = append(m.data, data...)

	for {
		l := uint32(len(m.data))
		if l < 2 {
			return nil
		}
		dataLen := uint32(binary.BigEndian.Uint16(m.data[0:2]))
		msgLen := dataLen + 2
		if l < msgLen {
			return nil
		}
		err := m.Next().ReceiveData(m.data[2:msgLen])
		m.data = m.data[msgLen:]
		if len(m.data) == 0 {
			m.data = nil
		}
		if err != nil {
			return err
		}
	}
}

func (m *Len2Data) SendData(data []byte) error {
	sendData := make([]byte, 2, 2+len(data))
	binary.BigEndian.PutUint16(sendData[:2], uint16(len(data)))
	sendData = append(sendData, data...)
	return m.Pre().SendData(sendData)
}
