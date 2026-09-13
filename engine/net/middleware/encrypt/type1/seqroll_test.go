package type1NetEncrypt

import (
	"errors"
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	len4Data "ztunnel/engine/net/middleware/package/len4Data"
	"ztunnel/testutil"
)

// 回归 M-10：`GoNextCsNo()` 与掩码加密发生在 `Pre().SendData()` **之前**。
// 一旦交付失败，本地序号已经推进而对端从未收到那一帧，双方从此永久错位。
//
// 批次1 引入 ErrFull（背压）之后，这不再是理论问题：队列满时连接可以仍然存活，
// 本帧却没出去。现在的契约是"交付失败则回滚序号"。

func newClientInHandsFinish(t *testing.T, send func([]byte) error) (*ClientNetEncrypt, netMiddleware.Middleware) {
	t.Helper()
	first, last := testutil.BuildChain(send,
		[]netMiddleware.CreateMiddlewareFunc{len4Data.NewMiddleware, NewClientNetEncrypt},
		func([]byte) error { return nil }, nil)
	cli := first.Next().Next().(*ClientNetEncrypt) // 链：first -> len4 -> type1(client) -> last
	cli.mu.Lock()
	cli.status = ClientStatusHandsFinish
	cli.Key1, cli.Key2, cli.CsNo, cli.ScNo = 1, 2, 100, 200
	cli.mu.Unlock()
	return cli, last
}

// 交付失败：序号必须回到原值。
func TestType1_ClientSendDataFailureRollsBackSequence(t *testing.T) {
	testutil.SilentLog(t)

	boom := errors.New("queue full")
	cli, last := newClientInHandsFinish(t, func([]byte) error { return boom })

	before := cli.CsNo
	err := last.SendData([]byte("payload"))
	testutil.Error(t, err, "交付失败必须上抛")
	testutil.Equal(t, uint64(before), uint64(cli.CsNo),
		"M-10 未修复：交付失败后序号仍被推进，将与对端永久错位")
}

// 交付成功：序号正常推进（回滚逻辑没把正常路径也吞掉）。
func TestType1_ClientSendDataSuccessAdvancesSequence(t *testing.T) {
	testutil.SilentLog(t)

	cli, last := newClientInHandsFinish(t, func([]byte) error { return nil })

	before := cli.CsNo
	testutil.NoError(t, last.SendData([]byte("payload")))
	testutil.True(t, cli.CsNo != before, "成功交付后序号应推进")
}

// 失败后紧接一次成功交付，序号只推进一次——即回滚没有"丢一步"。
func TestType1_ClientRollbackKeepsSequenceContiguous(t *testing.T) {
	testutil.SilentLog(t)

	fail := true
	cli, last := newClientInHandsFinish(t, func([]byte) error {
		if fail {
			return errors.New("transient failure")
		}
		return nil
	})

	s0 := cli.CsNo
	_ = last.SendData([]byte("a")) // 失败 → 回滚
	fail = false
	testutil.NoError(t, last.SendData([]byte("b"))) // 成功

	// 与"第一次就成功"的路径比较：序号必须停在同一步
	cli2, last2 := newClientInHandsFinish(t, func([]byte) error { return nil })
	testutil.NoError(t, last2.SendData([]byte("b")))

	testutil.Equal(t, uint64(cli2.CsNo), uint64(cli.CsNo),
		"M-10 未修复：失败一次后序号步长与正常路径不一致")
	testutil.True(t, cli.CsNo != s0, "成功那次交付仍需推进序号")
}

// 服务端侧对称行为。
func TestType1_ServerSendDataFailureRollsBackSequence(t *testing.T) {
	testutil.SilentLog(t)

	first, last := testutil.BuildChain(func([]byte) error { return errors.New("queue full") },
		[]netMiddleware.CreateMiddlewareFunc{len4Data.NewMiddleware, CreateServerNetEncryptFunc()},
		func([]byte) error { return nil }, nil)
	srv := first.Next().Next().(*ServerNetEncrypt)
	srv.mu.Lock()
	srv.status = ServerStatusHandsFinish
	srv.Key1, srv.Key2, srv.CsNo, srv.ScNo = 1, 2, 100, 200
	srv.mu.Unlock()

	before := srv.ScNo
	testutil.Error(t, last.SendData([]byte("payload")), "交付失败必须上抛")
	testutil.Equal(t, uint64(before), uint64(srv.ScNo),
		"M-10 未修复：server 交付失败后 ScNo 仍被推进")
}
