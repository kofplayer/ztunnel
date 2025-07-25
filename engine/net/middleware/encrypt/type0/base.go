package type0NetEncrypt

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

type BaseNetEncrypt struct {
	netMiddleware.MiddlewareBase
}

func (m *BaseNetEncrypt) OnEvent(e netMiddleware.MiddlewareEvent) {
	m.MiddlewareBase.OnEvent(e)
	switch e {
	case netMiddleware.MiddlewareEventOnConnect:
		m.FireEvent(netMiddleware.MiddlewareEventOnReady)
	}
}
