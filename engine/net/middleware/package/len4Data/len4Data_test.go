package packageLen4Data

import (
	"encoding/binary"
	"errors"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

func frame(payload []byte) []byte {
	out := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
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

func TestLen4_SingleFrame(t *testing.T) {
	rec, feed := newChain(t)
	testutil.NoError(t, feed(frame([]byte("abcd"))))
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, []byte("abcd"), rec.Get(0))
}

func TestLen4_PartialLengthPrefix(t *testing.T) {
	rec, feed := newChain(t)
	full := frame([]byte("abcd"))
	testutil.NoError(t, feed(full[:2]))
	testutil.Equal(t, 0, rec.Count(), "长度前缀不完整时不应分发")
	testutil.NoError(t, feed(full[2:]))
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, []byte("abcd"), rec.Get(0))
}

func TestLen4_MultipleFramesOneRead(t *testing.T) {
	rec, feed := newChain(t)
	batch := append(frame([]byte("aa")), frame([]byte("bbbb"))...)
	testutil.NoError(t, feed(batch))
	testutil.Equal(t, 2, rec.Count())
	testutil.BytesEqual(t, []byte("aa"), rec.Get(0))
	testutil.BytesEqual(t, []byte("bbbb"), rec.Get(1))
}

func TestLen4_ZeroLengthFrame(t *testing.T) {
	rec, feed := newChain(t)
	testutil.NoError(t, feed(frame(nil)))
	testutil.Equal(t, 1, rec.Count())
	testutil.Equal(t, 0, len(rec.Get(0)))
}

func TestLen4_ErrorStopsProcessing(t *testing.T) {
	rec := &testutil.Recorder{}
	first, _ := testutil.BuildChain(func([]byte) error { return nil },
		[]netMiddleware.CreateMiddlewareFunc{NewMiddleware},
		rec.ReceiveError(errors.New("downstream boom")), nil)
	batch := append(frame([]byte("aa")), frame([]byte("bb"))...)
	err := first.ReceiveData(batch)
	testutil.Error(t, err, "下游错误应向上传播")
	testutil.Equal(t, 1, rec.Count(), "下游出错后不应继续处理后续帧")
}

// ---- 回归用例（修复前红） ----

// 回归 #1：uint32 长度回绕。攻击者发送单个 4 字节包 FF FF FF FF，
// msgLen = 0xFFFFFFFF+4 回绕为 3 → m.data[4:3] 越界 panic，且全项目无 recover，
// 进程直接崩溃——任意未认证 TCP 连接可打挂服务端。
// 修复后：应返回错误或关闭连接，不得 panic。
func TestLen4_LengthWraparound_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		rec, feed := newChain(t)
		_ = rec
		_ = feed([]byte{0xFF, 0xFF, 0xFF, 0xFF})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #1 未修复：长度前缀回绕导致 panic（4 字节可远程崩溃服务端）:\n%v", err)
	}
}

// 回归 #1 加固：帧长度必须有上限，超限立即报错，
// 否则攻击者声明 4GB 长度后缓慢喂包可造成内存耗尽。
func TestLen4_OversizeFrame_Rejected(t *testing.T) {
	rec, feed := newChain(t)
	_ = rec
	// 声明约 2GB 的帧但只发长度前缀：当前实现会静默等待累积（内存耗尽风险）
	err := feed([]byte{0x7F, 0xFF, 0xFF, 0xFF})
	testutil.Error(t, err, "回归 #1 加固未实现：超长帧应立即拒绝而非等待累积")
}

// 回归 #5：分包层不得别名调用方缓冲。receiverRun 复用同一 4096 字节缓冲，
// 未拷贝时已转发给下游（进入发送队列）的帧会被下一次 Read 覆盖 → 静默数据损坏。
func TestLen4_NoBufferAliasing(t *testing.T) {
	rec, feed := newChain(t)
	buf := make([]byte, 0, 64)
	buf = append(buf, frame([]byte("ABCD"))...)
	testutil.NoError(t, feed(buf))
	testutil.Equal(t, 1, rec.Count())

	// 模拟 receiverRun 复用缓冲读下一段数据：整体改写调用方缓冲
	for i := range buf {
		buf[i] = 0xEE
	}
	testutil.BytesEqual(t, []byte("ABCD"), rec.Get(0),
		"回归 #5：已转发帧的内存被调用方缓冲覆盖（分包层未拷贝）")

	// 之后继续收数据也不得影响已转发帧
	testutil.NoError(t, feed(frame([]byte("EF"))))
	testutil.BytesEqual(t, []byte("ABCD"), rec.Get(0))
}
