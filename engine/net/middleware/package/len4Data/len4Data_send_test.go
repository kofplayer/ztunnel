package packageLen4Data

import (
	"encoding/binary"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// sendChain4 用 len4Data 单独成链，捕获写到"线上"的字节。
func sendChain4(t *testing.T) (m netMiddleware.Middleware, wire *[]byte) {
	t.Helper()
	var captured []byte
	unexpected := func([]byte) error { return errSendOnly }
	_, last := testutil.BuildChain(func(b []byte) error {
		captured = append(captured, b...)
		return nil
	}, []netMiddleware.CreateMiddlewareFunc{NewMiddleware}, unexpected, nil)
	return last, &captured
}

var errSendOnly = &sendOnlyError{}

type sendOnlyError struct{}

func (*sendOnlyError) Error() string { return "该链只用于发送方向，不应收到数据" }

// SendData 必须加 4 字节大端长度前缀，且长度只算 data 部分。
func TestLen4_SendData_PrependsBigEndianLength(t *testing.T) {
	m, wire := sendChain4(t)

	payload := []byte("hello")
	testutil.NoError(t, m.SendData(payload))

	want := make([]byte, 0, 4+len(payload))
	want = binary.BigEndian.AppendUint32(want, uint32(len(payload)))
	want = append(want, payload...)
	testutil.BytesEqual(t, want, *wire)
}

func TestLen4_SendData_EmptyPayload(t *testing.T) {
	m, wire := sendChain4(t)
	testutil.NoError(t, m.SendData([]byte{}))
	testutil.BytesEqual(t, []byte{0, 0, 0, 0}, *wire)
}

// 回归 L-3 同族：接收侧有 MaxFrameSize 上限，发送侧此前没有对应校验。
// 本端送出超限帧的后果是**对端**拒收并拆掉整条连接——失败点与原因不同侧，
// 排障时会看到"莫名其妙断线"而不是"我这帧太大"。现在在本端就拒绝。
func TestLen4_SendData_RejectsOversizePayload(t *testing.T) {
	// 边界内：正好等于上限
	okPayload := make([]byte, MaxFrameSize)
	m1, wire1 := sendChain4(t)
	testutil.NoError(t, m1.SendData(okPayload), "等于 MaxFrameSize 的载荷应合法")
	testutil.Equal(t, 4+len(okPayload), len(*wire1), "合法帧应带 4 字节前缀")

	// 越界：报错且不写出任何字节
	m2, wire2 := sendChain4(t)
	oversize := make([]byte, MaxFrameSize+1)
	testutil.Error(t, m2.SendData(oversize),
		"L-3 同族未修复：超过 MaxFrameSize 的帧被发出，只会在对端触发断连")
	testutil.Equal(t, 0, len(*wire2), "被拒绝的帧不得写出任何字节")
}

// 自洽性：SendData 产出的帧必须能被接收侧解开（含空载荷帧）。
func TestLen4_SendThenReceive_RoundTrips(t *testing.T) {
	rec, feed := newChain(t)
	m, wire := sendChain4(t)

	for _, payload := range [][]byte{
		{},                        // 空帧
		[]byte("x"),               // 单字节
		[]byte("control payload"), // 常规
	} {
		*wire = (*wire)[:0]
		rec.Reset()
		testutil.NoError(t, m.SendData(payload))
		testutil.NoError(t, feed(*wire), "自己发出的帧必须能被接收侧解开")
		testutil.Equal(t, 1, rec.Count())
		testutil.BytesEqual(t, payload, rec.Get(0))
	}
}
