package common

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

type SendDataFunc func(data []byte) error

func NewMiddlewareFirst(sendDataFunc SendDataFunc) netMiddleware.Middleware {
	return &NetMiddlewareFirst{
		sendDataFunc: sendDataFunc,
	}
}

type NetMiddlewareFirst struct {
	netMiddleware.MiddlewareBase
	sendDataFunc SendDataFunc
	events       []netMiddleware.MiddlewareEvent
	firingEvent  bool
}

func (m *NetMiddlewareFirst) SendData(bytes []byte) error {
	return m.sendDataFunc(bytes)
}

func (m *NetMiddlewareFirst) FireEvent(e netMiddleware.MiddlewareEvent) {
	if m.firingEvent {
		m.events = append(m.events, e)
		return
	}
	m.firingEvent = true
	m.OnEvent(e)
	for {
		if len(m.events) == 0 {
			break
		}
		events := m.events
		m.events = nil
		for _, event := range events {
			m.OnEvent(event)
		}
	}
	m.firingEvent = false
}
