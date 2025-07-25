package type0NetEncrypt

import (
	netMiddleware "ztunnel/engine/net/middleware"
)

func NewClientNetEncrypt() netMiddleware.Middleware {
	return &ClientNetEncrypt{}
}

type ClientNetEncrypt struct {
	BaseNetEncrypt
}
