package netMiddlewareVerifier

import (
	"errors"
	netMiddleware "ztunnel/engine/net/middleware"
)

func NewMiddleware() netMiddleware.Middleware {
	return &Verifier{}
}

type Verifier struct {
	netMiddleware.MiddlewareBase
}

func (m *Verifier) ReceiveData(data []byte) error {
	dataLen := len(data) - 1
	if dataLen < 0 {
		return errors.New("data len is not enough")
	}
	c := byte(dataLen & 0xFF)
	for _, v := range data {
		c ^= v
	}
	if c != 0 {
		return errors.New("data verify fail")
	}
	return m.MiddlewareBase.ReceiveData(data[:dataLen])
}

// SendData 在载荷尾部追加 1 字节校验位。
//
// 修复 M-16：原先是 `data = append(data, c)`。当调用方切片 **cap > len** 时，
// append 会把校验字节**就地写进调用方的底层数组**——而那块数组很可能正被另一条
// 连接的异步发送队列持有（帧是零拷贝入队的），于是变成静默数据损坏。
// 现在显式新建缓冲，绝不修改入参。
func (m *Verifier) SendData(data []byte) error {
	dataLen := len(data)
	out := make([]byte, dataLen, dataLen+1)
	copy(out, data)
	c := byte(dataLen & 0xFF)
	for _, v := range data {
		c ^= v
	}
	out = append(out, c)
	return m.MiddlewareBase.SendData(out)
}
