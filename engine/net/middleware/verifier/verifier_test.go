package netMiddlewareVerifier

import (
	"fmt"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// newChain 返回：rec=接收侧记录、sent=发送侧记录（链尾 SendData 的出口）、链首尾引用。
func newChain(t *testing.T) (rec, sent *testutil.Recorder, first, last netMiddleware.Middleware) {
	t.Helper()
	rec = &testutil.Recorder{}
	sent = &testutil.Recorder{}
	first, last = testutil.BuildChain(sent.Receive,
		[]netMiddleware.CreateMiddlewareFunc{NewMiddleware}, rec.Receive, nil)
	return rec, sent, first, last
}

// 基线：Send 追加校验字节，Receive 校验并剥离，还原原始数据。
func TestVerifier_Roundtrip(t *testing.T) {
	rec, sent, first, last := newChain(t)
	msg := []byte("hello verifier")
	testutil.NoError(t, last.SendData(msg))
	testutil.Equal(t, 1, sent.Count())
	framed := sent.Get(0)
	testutil.Equal(t, len(msg)+1, len(framed), "发送侧应追加 1 字节校验")

	testutil.NoError(t, first.ReceiveData(framed))
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, msg, rec.Get(0), "接收侧应还原原始数据")
}

// 基线：任意单比特翻转必须被检出（穷举校验，锁死完整性语义）。
// 注意：这是 XOR 校验和，只防位翻转，不是 MAC、不能防主动篡改（报告 #3）。
func TestVerifier_AllSingleBitFlipsDetected(t *testing.T) {
	rec, sent, first, last := newChain(t)
	_ = rec
	msg := make([]byte, 64)
	for i := range msg {
		msg[i] = byte(i * 7)
	}
	testutil.NoError(t, last.SendData(msg))
	framed := append([]byte(nil), sent.Get(0)...)

	for i := 0; i < len(framed); i++ {
		for bit := 0; bit < 8; bit++ {
			flipped := append([]byte(nil), framed...)
			flipped[i] ^= 1 << bit
			if err := first.ReceiveData(flipped); err == nil {
				t.Fatalf("字节 %d 的 bit %d 翻转未被检出", i, bit)
			}
		}
	}
}

// 边界：空数据与 1 字节数据。
func TestVerifier_BoundarySizes(t *testing.T) {
	for _, size := range []int{0, 1} {
		t.Run(fmt.Sprintf("size%d", size), func(t *testing.T) {
			rec, sent, first, last := newChain(t)
			msg := make([]byte, size)
			testutil.NoError(t, last.SendData(msg))
			framed := append([]byte(nil), sent.Get(0)...)
			testutil.NoError(t, first.ReceiveData(framed))
			testutil.Equal(t, 1, rec.Count())
			testutil.Equal(t, size, len(rec.Get(0)))
		})
	}
}
