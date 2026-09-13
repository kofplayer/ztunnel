package proto

import (
	"testing"

	netSession "ztunnel/engine/net/session"
)

// 每个用例自己保存/恢复全局，避免同包用例互相污染。
func withToken(t *testing.T, token string) {
	t.Helper()
	oldTok, oldLen := Token, TokenLen
	t.Cleanup(func() { Token, TokenLen = oldTok, oldLen })
	SetToken(token)
}

func TestSetToken_KeepsLenInSync(t *testing.T) {
	withToken(t, "s3cr3t")
	if TokenLen != 6 || Token != "s3cr3t" {
		t.Fatalf("SetToken 应同步 TokenLen: token=%q len=%d", Token, TokenLen)
	}

	withToken(t, "")
	if TokenLen != 0 {
		t.Fatalf("空 token 的 TokenLen 应为 0, got %d", TokenLen)
	}
}

// 鉴权比对必须走常量时间比较，且长度不符/内容不符都要被拒绝。
func TestTokenMatches(t *testing.T) {
	cases := []struct {
		name  string
		token string // 服务端配置的 token
		got   string // 客户端送来的 token
		want  bool
	}{
		{"完全匹配", "abcdef", "abcdef", true},
		{"单字符不符", "abcdef", "abcdeg", false},
		{"首字符不符", "abcdef", "zbcdef", false},
		{"长度不足", "abcdef", "abc", false},
		{"长度超长", "abcdef", "abcdefg", false},
		{"空 token 对空 token", "", "", true},
		{"空配置但送来非空", "", "x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withToken(t, tc.token)
			if got := TokenMatches(tc.got); got != tc.want {
				t.Fatalf("TokenMatches(%q) = %v, 期望 %v", tc.got, got, tc.want)
			}
		})
	}
}

// connectId 是协议里唯一的会话标识，大端编码必须在两端一致。
func TestSessionId_BigEndianRoundTrip(t *testing.T) {
	cases := []netSession.SessionID{0, 1, 255, 256, 65535, 0xDEADBEEF, 0xFFFFFFFF}
	for _, id := range cases {
		buf := make([]byte, netSession.SessionIDSize)
		WriteSessionId(buf, id)
		if got := ReadSessionId(buf); got != id {
			t.Fatalf("SessionID %d 编解码后变成 %d (bytes % x)", id, got, buf)
		}
	}

	// 必须是**大端**：协议约定所有字段大端序
	buf := make([]byte, netSession.SessionIDSize)
	WriteSessionId(buf, 0x01020304)
	want := []byte{0x01, 0x02, 0x03, 0x04}
	for i := range want {
		if buf[i] != want[i] {
			t.Fatalf("字节序不是大端: got % x, want % x", buf, want)
		}
	}
}

// SessionIDSize 被协议当作 connectId 的宽度使用，改它会静默破坏线格式。
func TestSessionIDSize_IsFourBytes(t *testing.T) {
	if netSession.SessionIDSize != 4 {
		t.Fatalf("connectId 必须占 4 字节，当前 SessionIDSize=%d", netSession.SessionIDSize)
	}
}

// 错误码取值被协议硬编码，改动会造成两端语义错位。
func TestErrorCodeValues(t *testing.T) {
	if ErrorCodeNone != 0 {
		t.Fatalf("ErrorCodeNone 必须是 0，got %d", ErrorCodeNone)
	}
	if ErrorCodeFailed == ErrorCodeNone {
		t.Fatal("失败码不能与成功码同值")
	}
}

// 消息 ID 是 1 字节 codec 承载的，>255 会被静默截断——这里固化当前取值范围。
func TestMsgIdValues_AreStableAndFitUint8(t *testing.T) {
	ids := map[string]uint32{
		"CreateTunnel":  MsgIdCreateTunnel,
		"ConnectNew":    MsgIdConnectNew,
		"ConnectData":   MsgIdConnectData,
		"ConnectDelete": MsgIdConnectDelete,
	}
	seen := map[uint32]string{}
	for name, id := range ids {
		if id == 0 || id > 255 {
			t.Fatalf("%s 的 msgId=%d 不在 1 字节可用范围内", name, id)
		}
		if dup, ok := seen[id]; ok {
			t.Fatalf("%s 与 %s 共用 msgId %d", name, dup, id)
		}
		seen[id] = name
	}
}
