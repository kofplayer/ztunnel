package netMiddleware_test

import (
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
	"ztunnel/testutil"
)

// solo 是一个只嵌入了 MiddlewareBase 的中间件，不接入任何链。
type solo struct {
	netMiddleware.MiddlewareBase
	events int
}

// OnEvent 先向下游传播再计数，与真实中间件（如 type1）的写法一致。
func (s *solo) OnEvent(e netMiddleware.MiddlewareEvent) {
	s.MiddlewareBase.OnEvent(e)
	s.events++
}

// 回归 CRASH-04：`MiddlewareBase.FireEvent` 原实现是 `m.First().FireEvent(e)`，
// 而 `First()` 沿 pre 回溯、**在 pre 为 nil 时返回 m 自身**。于是链头不是
// NetMiddlewareFirst 的任何情形（中间件被单独使用、链未接完整、工厂返回 nil
// 造成接线残缺）都会重新进入同一个 FireEvent → 无限自我递归 → stack overflow。
//
// 这比普通的 nil panic 更糟：Go 的栈溢出是 fatal error，**recover 拦不住**，
// 因此 conn.go 的 recoverPanic / safeAccept 兜底全部失效，进程直接死。
//
// 用崩溃探针而非直接调用：栈溢出会带走整个测试二进制，无法被断言捕获。
func TestMiddleware_FireEventOnUnwiredChain_NoStackOverflow(t *testing.T) {
	if testutil.InCrashProbe() {
		s := &solo{}
		s.FireEvent(netMiddleware.MiddlewareEventOnConnect)
		s.FireEvent(netMiddleware.MiddlewareEventOnReady)
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("CRASH-04 未修复：在未接完整的链上 FireEvent 导致无限自我递归与栈溢出（recover 拦不住）:\n%v", err)
	}
}

// 守住修复的副作用：接成完整链后，事件仍必须回溯到链头再沿 Next 逐级传播，
// 不能被本地退化分支截胡。
//
// 期望值 head=0 / mid=1 / tail=1 的依据：本地退化分支里的 `m.OnEvent(e)` 接收者是
// 嵌入的 `*MiddlewareBase`，属 **Go 静态派发**，只会经 `MiddlewareBase.OnEvent`
// 向下游传播，不会调用外层类型对 OnEvent 的覆写——因此链头自己的计数不增加，
// 而以下游接口值传递的节点会正常收到。这是 Go 的既有语义，非本次修复引入。
// 生产链头 `NetMiddlewareFirst` 覆写的是 `FireEvent` 本身，所以根本不走这条分支。
func TestMiddleware_FireEventOnWiredChain_PropagatesFromHead(t *testing.T) {
	head := &solo{}
	mid := &solo{}
	tail := &solo{}

	head.SetNext(mid)
	mid.SetPre(head)
	mid.SetNext(tail)
	tail.SetPre(mid)

	// 从链尾发起：应回溯到 head，再由 head 向下游逐级派发
	tail.FireEvent(netMiddleware.MiddlewareEventOnConnect)

	testutil.Equal(t, 0, head.events, "静态派发不会调用链头自己的 OnEvent 覆写")
	testutil.Equal(t, 1, mid.events, "中间节点应恰好收到一次事件")
	testutil.Equal(t, 1, tail.events, "链尾应恰好收到一次事件")
}
