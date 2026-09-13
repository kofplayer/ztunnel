package proto

import (
	"crypto/subtle"
	"encoding/binary"
	netSession "ztunnel/engine/net/session"
)

// 线上帧格式（由分包中间件决定，非本包构造）：
//
//	len(4B BE, 大端, 只算 body 长度) | body
//	body = msgId(1B, 由 codec_type8_data 承载) | data [| verifier(1B)]
//
// 控制通道用 len4Data + codec_type8_data；数据通道用 fullData + codec_data（透传）。
// ⚠️ 协议无版本字段，任何格式变更都是断代改动。
const (
	MsgIdCreateTunnel  = 1 // c2s:token(TokenLen)+outport(2)   	s2c:errorcode(1)
	MsgIdConnectNew    = 2 // s2c:connectId(4) 				c2s:errorcode(1),connectId(4)
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

// TokenMatches 以**常量时间**比对待校验 token。
//
// 此前直接用 `token != proto.Token`：Go 的字符串相等是逐字节短路比较，
// 第一个不匹配字节即返回，属可被计时观测的鉴权判断。改用标准库
// crypto/subtle（仍满足零第三方依赖）。
// 长度不等时也用一次等长比较吃掉时间差，避免通过响应时间判别 token 长度。
func TokenMatches(got string) bool {
	b := []byte(got)
	if len(b) != TokenLen {
		// 用等长的一次假比较掩盖"长度不符"这一快速失败信号
		buf := make([]byte, TokenLen)
		subtle.ConstantTimeCompare(buf, buf)
		return false
	}
	return subtle.ConstantTimeCompare(b, []byte(Token)) == 1
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
