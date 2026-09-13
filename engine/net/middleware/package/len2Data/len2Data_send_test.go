package packageLen2Data

import (
	"encoding/binary"
	"fmt"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// 用 len2Data 单独成链，捕获它写到"线上"的字节。
func sendChain(t *testing.T) (m netMiddleware.Middleware, wire *[]byte) {
	t.Helper()
	var captured []byte
	unexpected := func([]byte) error {
		return fmt.Errorf("发送链不应收到数据")
	}
	_, last := testutil.BuildChain(func(b []byte) error {
		captured = append(captured, b...)
		return nil
	}, []netMiddleware.CreateMiddlewareFunc{NewMiddleware}, unexpected, nil)
	return last, &captured
}

// SendData 此前完全无测试（0%）：它负责给载荷加 2 字节大端长度前缀。
func TestLen2_SendData_PrependsBigEndianLength(t *testing.T) {
	m, wire := sendChain(t)

	payload := []byte("abcde")
	testutil.NoError(t, m.SendData(payload))

	want := make([]byte, 0, 2+len(payload))
	want = binary.BigEndian.AppendUint16(want, uint16(len(payload)))
	want = append(want, payload...)
	testutil.BytesEqual(t, want, *wire)
}

func TestLen2_SendData_EmptyPayload(t *testing.T) {
	m, wire := sendChain(t)
	testutil.NoError(t, m.SendData([]byte{}))
	testutil.BytesEqual(t, []byte{0, 0}, *wire)
}

// 回归 L-3（已修复）：发送侧原先直接 `uint16(len(data))`，载荷超过 65535 字节时
// 长度静默回绕，对端按错的长度解析、把剩余字节当成新帧头 → 协议永久错乱且本端
// 不报任何错。现在必须拒绝。
func TestLen2_SendData_RejectsOversizePayload(t *testing.T) {
	// 边界内合法：正好 65535 字节
	okPayload := make([]byte, MaxFrameSize)
	m1, wire1 := sendChain(t)
	testutil.NoError(t, m1.SendData(okPayload), "65535 字节应合法")
	testutil.Equal(t, 2+len(okPayload), len(*wire1), "合法帧应带 2 字节前缀")
	testutil.Equal(t, byte(0xFF), (*wire1)[0], "前缀高字节应为 0xFF")
	testutil.Equal(t, byte(0xFF), (*wire1)[1], "前缀低字节应为 0xFF")

	// 越界必须报错，且不得写出任何字节
	m2, wire2 := sendChain(t)
	err := m2.SendData(make([]byte, MaxFrameSize+1))
	testutil.Error(t, err, "L-3 未修复：超过 2 字节前缀上限的载荷被静默截断而非报错")
	testutil.Equal(t, 0, len(*wire2), "被拒绝的帧不得写出任何字节")

	// 明显越界：确认判定没写反（旧实现会把 128K+10 报成 10）
	m3, wire3 := sendChain(t)
	testutil.Error(t, m3.SendData(make([]byte, 128*1024+10)), "128KB 载荷必须被拒绝")
	testutil.Equal(t, 0, len(*wire3), "被拒绝的帧不得写出任何字节")
}

// SendData 不得就地改写调用方的切片——它必须自己分配带前缀的缓冲。
func TestLen2_SendData_DoesNotMutateCallerSlice(t *testing.T) {
	m, _ := sendChain(t)

	payload := append([]byte("xyz"), make([]byte, 32)...) // cap 明显大于 len
	before := append([]byte(nil), payload...)
	_ = m.SendData(payload)

	testutil.Equal(t, len(before), len(payload), "调用方切片长度被改动")
	for i := range before {
		testutil.Equal(t, before[i], payload[i], "调用方字节被就地改写; index", i)
	}
}

// 收方向也要能处理 SendData 产出的帧（自洽性）。
func TestLen2_SendThenReceive_RoundTrips(t *testing.T) {
	rec, feed := newChain(t)
	m, wire := sendChain(t)

	payload := []byte("round trip")
	testutil.NoError(t, m.SendData(payload))
	testutil.NoError(t, feed(*wire))

	testutil.Equal(t, 1, rec.Count(), "对端应收到 1 帧")
	testutil.BytesEqual(t, payload, rec.Get(0))
}
