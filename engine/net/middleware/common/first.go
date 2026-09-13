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

	// 复位必须放进 defer：OnEvent 可能 panic（type1 的状态守卫就是显式 panic）。
	// 那样 firingEvent 会永远停在 true，而执行它的 goroutine 已经没了 ——
	// 此后这条链上的所有事件（包括 OnDisconnect）只入队、永不派发，
	// 中间件的握手状态与密钥字段也不做任何清理（报告 CRASH-03）。
	//
	// 已排队的事件**保留**不清空：上游的 recoverPanic 会紧接着触发 OnDisconnect，
	// 届时 firingEvent 已复位，那次派发会顺手把积压事件一起排空。
	defer func() {
		m.mu.Lock()
		m.firingEvent = false
		m.mu.Unlock()
	}()

	// OnEvent 必须在锁外执行：回调中可能再次 FireEvent（由 firingEvent
	// 标记处理重入），也可能发起同连接的 SendData。
	m.OnEvent(e)
	for {
		m.mu.Lock()
		if len(m.events) == 0 {
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
