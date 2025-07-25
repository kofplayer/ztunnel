package common

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

type OnReceiveDataFunc func(data []byte) error
type OnReadyFunc func()

func NewMiddlewareLast(onReceiveData OnReceiveDataFunc, onReady OnReadyFunc) netMiddleware.Middleware {
	return &NetMiddlewareLast{
		onReceiveData: onReceiveData,
		onReady:       onReady,
	}
}

type NetMiddlewareLast struct {
	netMiddleware.MiddlewareBase
	onReceiveData OnReceiveDataFunc
	onReady       OnReadyFunc
}

func (m *NetMiddlewareLast) ReceiveData(bytes []byte) error {
	return m.onReceiveData(bytes)
}

func (m *NetMiddlewareLast) OnEvent(e netMiddleware.MiddlewareEvent) {
	m.MiddlewareBase.OnEvent(e)
	switch e {
	case netMiddleware.MiddlewareEventOnReady:
		if m.onReady != nil {
			m.onReady()
		}
	}
}
