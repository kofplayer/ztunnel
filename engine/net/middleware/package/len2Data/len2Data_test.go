package packageLen2Data

import (
	"encoding/binary"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

func frame2(payload []byte) []byte {
	out := make([]byte, 2, 2+len(payload))
	binary.BigEndian.PutUint16(out, uint16(len(payload)))
	return append(out, payload...)
}

func newChain(t *testing.T) (rec *testutil.Recorder, feed func([]byte) error) {
	t.Helper()
	rec = &testutil.Recorder{}
	first, _ := testutil.BuildChain(func([]byte) error { return nil },
		[]netMiddleware.CreateMiddlewareFunc{NewMiddleware}, rec.Receive, nil)
	return rec, first.ReceiveData
}

// ---- 基线行为固化 ----
// 说明：len2Data 当前无调用方（报告 #20 死代码清单），若保留需维持正确性，若删除则连同测试一起删除。

func TestLen2_SingleFrame(t *testing.T) {
	rec, feed := newChain(t)
	testutil.NoError(t, feed(frame2([]byte("abcd"))))
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, []byte("abcd"), rec.Get(0))
}

func TestLen2_PartialLengthPrefix(t *testing.T) {
	rec, feed := newChain(t)
	full := frame2([]byte("abcd"))
	testutil.NoError(t, feed(full[:1]))
	testutil.Equal(t, 0, rec.Count())
	testutil.NoError(t, feed(full[1:]))
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, []byte("abcd"), rec.Get(0))
}

func TestLen2_MultipleFramesOneRead(t *testing.T) {
	rec, feed := newChain(t)
	batch := append(frame2([]byte("aa")), frame2([]byte("bbbb"))...)
	testutil.NoError(t, feed(batch))
	testutil.Equal(t, 2, rec.Count())
	testutil.BytesEqual(t, []byte("aa"), rec.Get(0))
	testutil.BytesEqual(t, []byte("bbbb"), rec.Get(1))
}

// ---- 回归用例（修复前红） ----

// 回归 #5：与 len4Data 相同的缓冲区别名问题。
// 注：uint16 长度最大 65537，无回绕风险，故无 wraparound 用例。
func TestLen2_NoBufferAliasing(t *testing.T) {
	rec, feed := newChain(t)
	buf := make([]byte, 0, 64)
	buf = append(buf, frame2([]byte("ABCD"))...)
	testutil.NoError(t, feed(buf))
	testutil.Equal(t, 1, rec.Count())

	for i := range buf {
		buf[i] = 0xEE
	}
	testutil.BytesEqual(t, []byte("ABCD"), rec.Get(0),
		"回归 #5：已转发帧的内存被调用方缓冲覆盖（分包层未拷贝）")
}
