package proto

import (
	"encoding/binary"
	netSession "ztunnel/engine/net/session"
)

// len(2) msgId(1) data
const (
	MsgLenSize = 2

	MsgIdNone          = 0
	MsgIdCreateTunnel  = 1 // c2s:outport(2)   					s2c:errorcode(1)
	MsgIdConnectNew    = 2 // s2c:connectId(4) 					c2s:errorcode(1),connectId(4)
	MsgIdConnectData   = 3 // s2c:connectId(4),data 			c2s:connectId(4),data
	MsgIdConnectDelete = 4 // s2c:connectId(4) 					c2s:connectId(4)
)

var (
	Token      = ""
	TokenLen   = 0
	NetEncrypt = false
)

func SetToken(token string) {
	Token = token
	TokenLen = len(token)
}

const (
	ErrorCodeNone   = 0
	ErrorCodeNormal = 1
)

func ReadSessionId(b []byte) netSession.SessionID {
	return netSession.SessionID(binary.BigEndian.Uint32(b))
}

func WriteSessionId(b []byte, v netSession.SessionID) {
	binary.BigEndian.PutUint32(b, uint32(v))
}
