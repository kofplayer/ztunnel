package type0NetEncrypt

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

func CreateServerNetEncryptFunc() netMiddleware.CreateMiddlewareFunc {
	return func() netMiddleware.Middleware {
		return &ServerNetEncrypt{}
	}
}

type ServerNetEncrypt struct {
	BaseNetEncrypt
}
