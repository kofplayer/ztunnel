package common

import (
	"sync"

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
	// 事件可能来自不同 goroutine：OnConnect 出自拨号/accept goroutine，
	// OnReady/OnDisconnect 出自接收 goroutine，二者可并发触发，
	// 重入队列必须加锁，否则 events/firingEvent 存在数据竞争。
	mu          sync.Mutex
	events      []netMiddleware.MiddlewareEvent
	firingEvent bool
}

func (m *NetMiddlewareFirst) SendData(bytes []byte) error {
	return m.sendDataFunc(bytes)
}

func (m *NetMiddlewareFirst) FireEvent(e netMiddleware.MiddlewareEvent) {
	m.mu.Lock()
	if m.firingEvent {
		m.events = append(m.events, e)
		m.mu.Unlock()
		return
	}
	m.firingEvent = true
	m.mu.Unlock()

	// OnEvent 必须在锁外执行：回调中可能再次 FireEvent（由 firingEvent
	// 标记处理重入），也可能发起同连接的 SendData。
	m.OnEvent(e)
	for {
		m.mu.Lock()
		if len(m.events) == 0 {
			m.firingEvent = false
			m.mu.Unlock()
			break
		}
		events := m.events
		m.events = nil
		m.mu.Unlock()
		for _, event := range events {
			m.OnEvent(event)
		}
	}
}
