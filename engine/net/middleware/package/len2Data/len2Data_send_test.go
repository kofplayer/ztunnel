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

// 表征测试（characterization）：**发送侧不校验 64KB 上限**（报告 L-3）。
// 载荷超过 65535 字节时 `uint16(len(data))` 静默回绕，对端会按错的长度解析、
// 把后续字节当成新帧头 → 协议错乱。len4Data 有 MaxFrameSize，len2Data 的
// 发送侧没有对应保护。本用例钉住当前行为；若加上限校验需同步更新断言。
func TestLen2_SendData_TruncatesOversizePayload(t *testing.T) {
	m, wire := sendChain(t)

	oversize := make([]byte, 65536+10)
	testutil.NoError(t, m.SendData(oversize))

	header := binary.BigEndian.Uint16((*wire)[:2])
	testutil.Equal(t, uint16(10), header,
		"当前实现把 65546 静默截断成 10（报告 L-3）；若已改为报错请更新本用例")
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
