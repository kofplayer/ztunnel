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

func (m *Verifier) SendData(data []byte) error {
	dataLen := len(data)
	c := byte(dataLen & 0xFF)
	for _, v := range data {
		c ^= v
	}
	data = append(data, c)
	return m.MiddlewareBase.SendData(data)
}
