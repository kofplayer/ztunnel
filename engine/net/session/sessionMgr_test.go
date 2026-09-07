// 注意：本包测试使用外部测试包（netSession_test），因为 testutil 依赖本包，
// 内部测试包会造成 import cycle。
package netSession_test

import (
	"sync"
	"testing"

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
