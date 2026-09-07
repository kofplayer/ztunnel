package type1NetEncrypt

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ztunnel/testutil"
)

// 回归 #6：-net_encrypt=true 时控制会话被多个 goroutine 并发 SendMessage
// （服务端每个用户连接的回调各自持有 receiver goroutine），而 type1 的
// SendData 序列 GoNextScNo → KeyMaskData 无任何锁：并发下掩码序列错乱，
// 接收端按消息数递增 CsNo → 永久失步 → 校验失败/数据损坏。
//
// 该用例用 32 goroutine × 500 条带序号载荷并发发送；修复（发送侧加锁或
// 单发送 goroutine）前必然出现失步/损坏，修复后全部消息完整且序号无重无漏。
func TestType1_ConcurrentSend_NoDesync(t *testing.T) {
	p := startPair(t)
	const goroutines, perG = 32, 2000
	// 注：该竞争的检出依赖调度时序（无 -race 时约 9/10 概率红），
	// 属概率性回归用例；-race 环境下（CI）为确定性检出。

	var wg sync.WaitGroup
	sendErrs := make(chan error, goroutines)
	var seq atomic.Uint32
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				msg := testutil.SeqPayload(seq.Add(1), 64)
				if err := p.cLast.SendData(msg); err != nil {
					sendErrs <- err
					return
				}
			}
		}()
	}
	sendDone := make(chan struct{})
	go func() { wg.Wait(); close(sendDone) }()

	select {
	case err := <-p.pumpErrS:
		t.Fatalf("回归 #6 未修复：并发发送导致加解密失步（服务端校验/解密失败）: %v", err)
	case <-sendDone:
	case <-time.After(30 * time.Second):
		t.Fatalf("回归 #6 未修复：发送方 30s 未完成（接收端失步后卡死）")
	}
	close(sendErrs)
	for err := range sendErrs {
		testutil.NoError(t, err, "回归 #6：并发发送出现失败")
	}

	testutil.True(t, testutil.Eventually(t, 10*time.Second, func() bool {
		return p.srec.Count() == goroutines*perG
	}), "回归 #6：服务端消息数不符, got", p.srec.Count(), "want", goroutines*perG)

	seen := make(map[uint32]bool, goroutines*perG)
	for i := 0; i < p.srec.Count(); i++ {
		s, err := testutil.VerifySeqPayload(p.srec.Get(i))
		if err != nil {
			t.Fatalf("回归 #6 未修复：第 %d 条消息损坏: %v", i, err)
		}
		if seen[s] {
			t.Fatalf("回归 #6 未修复：序号 %d 重复（发送侧掩码状态竞争）", s)
		}
		seen[s] = true
	}
}
