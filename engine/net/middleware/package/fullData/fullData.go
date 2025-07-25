package packageFullData

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

func NewMiddleware() netMiddleware.Middleware {
	return &FullData{}
}

type FullData struct {
	netMiddleware.MiddlewareBase
}
