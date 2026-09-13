package packageLen4Data

import (
	"encoding/binary"
	"fmt"

	netMiddleware "ztunnel/engine/net/middleware"
)

// MaxFrameSize 单帧长度上限。长度前缀是攻击者可控输入，必须设上限，
// 否则声明 4GB 长度后缓慢喂包可造成内存耗尽（报告 #1）。
const MaxFrameSize = 8 << 20 // 8MB

func NewMiddleware() netMiddleware.Middleware {
	return &Len4Data{}
}

type Len4Data struct {
	netMiddleware.MiddlewareBase
	data []byte
}

func (m *Len4Data) ReceiveData(data []byte) error {
	// 必须拷贝：调用方（receiverRun）复用同一读取缓冲，直接别名会让
	// 已转发给下游（进入发送队列）的帧被下一次读取覆盖（报告 #5）。
	if m.data == nil {
		m.data = make([]byte, 0, len(data)+64)
	}
	m.data = append(m.data, data...)

	for {
		l := uint64(len(m.data))
		if l < 4 {
			return nil
		}
		// 用 uint64 计算 msgLen：uint32 下 0xFFFFFFFF+4 会回绕为 3，
		// 触发 m.data[4:3] 越界 panic（报告 #1，4 字节可远程崩溃进程）。
		dataLen := uint64(binary.BigEndian.Uint32(m.data[0:4]))
		if dataLen > MaxFrameSize {
			return fmt.Errorf("frame size %d exceeds limit %d", dataLen, MaxFrameSize)
		}
		msgLen := dataLen + 4
		if l < msgLen {
			return nil
		}
		err := m.Next().ReceiveData(m.data[4:msgLen])
		m.data = m.data[msgLen:]
		if len(m.data) == 0 {
			// 释放底层大缓冲，避免长连接长期持有峰值容量
			m.data = nil
		}
		if err != nil {
			return err
		}
	}
}

func (m *Len4Data) SendData(data []byte) error {
	// 接收侧有 MaxFrameSize 上限，发送侧此前没有对应校验：本端送出超限帧的
	// 后果是**对端**拒收并拆掉整条连接，错误表现为对方的断连而不是本端的失败
	// （审计 L-3 同族问题）。在本端就拒绝，失败点才与原因同侧。
	if len(data) > MaxFrameSize {
		return fmt.Errorf("len4Data: frame size %d exceeds limit %d", len(data), MaxFrameSize)
	}
	sendData := make([]byte, 4, 4+len(data))
	binary.BigEndian.PutUint32(sendData[:4], uint32(len(data)))
	sendData = append(sendData, data...)
	return m.Pre().SendData(sendData)
}
