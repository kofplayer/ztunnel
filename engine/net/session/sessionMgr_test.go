// 注意：本包测试使用外部测试包（netSession_test），因为 testutil 依赖本包，
// 内部测试包会造成 import cycle。
package netSession_test

import (
	"sync"
	"testing"
	"time"

	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

func TestSessionMgr_Lifecycle(t *testing.T) {
	sm := netSession.NewSessionMgr()
	s1 := sm.NewSession()
	s2 := sm.NewSession()
	testutil.True(t, s1.GetID() != s2.GetID(), "会话 ID 应唯一")
	testutil.Equal(t, s1, sm.GetSession(s1.GetID()))

	sm.RemoveSession(s1.GetID())
	testutil.True(t, sm.GetSession(s1.GetID()) == nil, "移除后应查不到")
	testutil.True(t, sm.GetSession(s2.GetID()) != nil)
}

// 回归 M-11：TravelSession 必须"锁内快照、锁外回调"。
//
// SEC-01 修复后，回调链会经 Close → fireDisconnect → RemoveSession 去拿同一把
// 锁的**写锁**。RWMutex 不记录持有者，同 goroutine 持 RLock 时调 Lock() 会永远
// 等待自己释放读锁 —— 修复前这里是确定性自死锁，不是竞态。
func TestSessionMgr_TravelSession_CallbackMayRemove(t *testing.T) {
	sm := netSession.NewSessionMgr()
	for i := 0; i < 8; i++ {
		sm.NewSession()
	}
	testutil.Equal(t, 8, sm.Len(), "Len 应反映当前会话数（报告 E-09）")

	done := make(chan struct{})
	go func() {
		defer close(done)
		sm.TravelSession(func(s netSession.NetSession) bool {
			sm.RemoveSession(s.GetID())
			_ = sm.Len()
			sm.GetSession(s.GetID())
			return true
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("M-11 未修复：TravelSession 在持 RLock 期间执行回调，回调内取写锁导致自死锁")
	}
	testutil.Equal(t, 0, sm.Len(), "回调内的 RemoveSession 应全部生效")
}

// 回归 M-11 的提前终止语义：回调返回 false 应停止遍历。
func TestSessionMgr_TravelSession_EarlyStop(t *testing.T) {
	sm := netSession.NewSessionMgr()
	for i := 0; i < 5; i++ {
		sm.NewSession()
	}
	seen := 0
	sm.TravelSession(func(netSession.NetSession) bool {
		seen++
		return seen < 2
	})
	testutil.Equal(t, 2, seen)
}

// 回归（报告 #17 杂项）：genUId 在锁外自增，并发创建会话会产生重复 ID
// （重复 ID 意味着不同连接共享同一会话条目）。修复：自增移入锁内。
// 说明：无 -race 时该用例靠功能性重复检出竞争，几乎必然触发但非 100%。
func TestSessionMgr_ConcurrentNewSession_UniqueIDs(t *testing.T) {
	sm := netSession.NewSessionMgr()
	const goroutines, perG = 32, 250
	ids := make(chan netSession.SessionID, goroutines*perG)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ids <- sm.NewSession().GetID()
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[netSession.SessionID]int, goroutines*perG)
	for id := range ids {
		seen[id]++
	}
	if len(seen) != goroutines*perG {
		t.Fatalf("回归未修复：并发创建出现重复会话 ID（genUId 锁外自增），唯一 %d / 总 %d",
			len(seen), goroutines*perG)
	}
}
