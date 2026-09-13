package netMiddlewareVerifier

import (
	"errors"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// 说明：接收方向必须从**链头** first 喂入（ReceiveData 是 First→…→Last），
// 发送方向必须从**链尾** last 进入（SendData 是 Last→…→First）。
// 用错端点会直接绕过 verifier，测试就变成空跑。

func newPair(t *testing.T, rec *testutil.Recorder) (first, last netMiddleware.Middleware, wire *[]byte) {
	t.Helper()
	var captured []byte
	f, l := testutil.BuildChain(func(b []byte) error {
		captured = append(captured, b...)
		return nil
	}, []netMiddleware.CreateMiddlewareFunc{NewMiddleware}, rec.Receive, nil)
	return f, l, &captured
}

func checksum(data []byte) byte {
	c := byte(len(data) & 0xFF)
	for _, v := range data {
		c ^= v
	}
	return c
}

// 空数据（连校验字节都没有）必须报错。此前该分支无任何用例覆盖。
func TestVerifier_EmptyFrameIsRejected(t *testing.T) {
	rec := &testutil.Recorder{}
	first, _, _ := newPair(t, rec)

	testutil.Error(t, first.ReceiveData([]byte{}), "空帧必须被拒绝，不得越界取校验字节")
	testutil.Equal(t, 0, rec.Count(), "被拒绝的帧不应下发")
}

// 校验字节不符时必须报错并中断下发。
func TestVerifier_BadChecksumIsRejected(t *testing.T) {
	rec := &testutil.Recorder{}
	first, _, _ := newPair(t, rec)

	// 载荷 [0x01,0x02] 的正确校验位是 2^1^2=1，故意给错值
	testutil.Error(t, first.ReceiveData([]byte{0x01, 0x02, 0xFF}), "校验不符必须报错")
	testutil.Equal(t, 0, rec.Count(), "校验失败的帧不得下发")
}

// 合法帧必须剥掉校验字节后下发原载荷。
func TestVerifier_ValidFrameStripsChecksum(t *testing.T) {
	rec := &testutil.Recorder{}
	first, _, _ := newPair(t, rec)

	payload := []byte{0x0A, 0x0B, 0x0C}
	frame := append(append([]byte(nil), payload...), checksum(payload))

	testutil.NoError(t, first.ReceiveData(frame), "合法帧应通过校验")
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, payload, rec.Get(0), "下发内容应为剥掉校验字节的原载荷")
}

// 下游返回错误时必须原样上抛，让接收链中断。
func TestVerifier_PropagatesDownstreamError(t *testing.T) {
	sentinel := errors.New("downstream failed")
	var captured []byte
	first, _ := testutil.BuildChain(func(b []byte) error {
		captured = append(captured, b...)
		return nil
	}, []netMiddleware.CreateMiddlewareFunc{NewMiddleware},
		func([]byte) error { return sentinel }, nil)
	_ = captured

	payload := []byte{0x01}
	frame := append(append([]byte(nil), payload...), checksum(payload))
	err := first.ReceiveData(frame)
	testutil.Error(t, err, "下游错误必须上抛")
	if !errors.Is(err, sentinel) {
		t.Fatalf("应原样上抛下游错误, got %v", err)
	}
}

// 零长度载荷（只有一个校验字节）是合法帧，不应被误判为空数据。
func TestVerifier_ZeroLengthPayloadAllowed(t *testing.T) {
	rec := &testutil.Recorder{}
	first, _, _ := newPair(t, rec)

	frame := []byte{checksum(nil)} // dataLen=0 → 校验位 0
	testutil.NoError(t, first.ReceiveData(frame), "空载荷加校验位是合法帧")
	testutil.Equal(t, 1, rec.Count(), "空载荷帧也应下发一次")
	testutil.Equal(t, 0, len(rec.Get(0)), "下发内容应为空")
}

// 发送侧产出的帧必须是「载荷 + 1 字节校验位」，并能被接收侧还原。
func TestVerifier_SendRoundTripsThroughReceive(t *testing.T) {
	rec := &testutil.Recorder{}
	recvFirst, sendLast, wire := newPair(t, rec)

	payload := []byte{0x0A, 0x0B}
	testutil.NoError(t, sendLast.SendData(payload))

	// dataLen=2 → c = 2 ^ 0x0A ^ 0x0B = 0x03
	testutil.BytesEqual(t, []byte{0x0A, 0x0B, 0x03}, *wire, "线上帧应为载荷加校验位")

	// 自洽：同一算法必须验得回来
	testutil.NoError(t, recvFirst.ReceiveData(append([]byte(nil), *wire...)),
		"自己发出的帧必须能通过自己的校验")
	testutil.Equal(t, 1, rec.Count())
	testutil.BytesEqual(t, payload, rec.Get(0), "剥掉校验字节后应还原为原载荷")
}

// 表征测试（报告 M-16，**尚未修复**）：`SendData` 用 `data = append(data, c)`，
// 当调用方切片 cap > len 时会**就地**把校验字节写进调用方的底层数组。该数组
// 可能正被别的连接持有（例如从接收缓冲零拷贝进发送队列的那条路径），于是变成
// 静默数据损坏。修复后 spare[2] 应保持哨兵值，届时请更新本用例。
func TestVerifier_SendData_MutatesCallerBackingArray(t *testing.T) {
	var spare [32]byte
	for i := range spare {
		spare[i] = 0xEE // 哨兵值
	}

	var captured []byte
	_, last := testutil.BuildChain(func(b []byte) error {
		captured = append(captured, b...)
		return nil
	}, []netMiddleware.CreateMiddlewareFunc{NewMiddleware}, nil, nil)

	payload := spare[:2] // len=2, cap=32：留有富余
	payload[0], payload[1] = 0x0A, 0x0B

	testutil.NoError(t, last.SendData(payload))
	testutil.Equal(t, 3, len(captured), "线上帧应为 3 字节")
	testutil.Equal(t, byte(0x03), spare[2],
		"当前实现把校验字节就地写进了调用方的底层数组（M-16 未修复）；"+
			"若已改为新建缓冲，此处应保持哨兵值 0xEE，请同步更新本用例")
}
