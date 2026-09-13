package packageLen2Data

import (
	"encoding/binary"
	"fmt"

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

// MaxFrameSize 单帧载荷上限。接收侧由 uint16 长度天然受限，发送侧必须同样
// 校验，否则 `uint16(len(data))` 会静默回绕（报告 L-3）。
const MaxFrameSize = 0xFFFF // 64KB

func (m *Len2Data) SendData(data []byte) error {
	// 修复 L-3：原先直接 uint16(len(data))。载荷 >64KB 时长度回绕成小值，
	// 对端按错的长度取帧、把剩余字节当成新帧头 —— 协议永久错乱且没有任何报错。
	if len(data) > MaxFrameSize {
		return fmt.Errorf("len2Data: frame size %d exceeds 2-byte prefix limit %d", len(data), MaxFrameSize)
	}
	sendData := make([]byte, 2, 2+len(data))
	binary.BigEndian.PutUint16(sendData[:2], uint16(len(data)))
	sendData = append(sendData, data...)
	return m.Pre().SendData(sendData)
}
