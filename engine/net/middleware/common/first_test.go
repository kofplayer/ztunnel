package common_test

import (
	"testing"
	"time"

	netMiddleware "ztunnel/engine/net/middleware"
	netMiddlewareCommon "ztunnel/engine/net/middleware/common"
	"ztunnel/testutil"
)

// panicMW 在收到事件时 panic —— 对应 type1 状态守卫里的显式 panic。
type panicMW struct {
	netMiddleware.MiddlewareBase
	panics int
}

func (p *panicMW) OnEvent(netMiddleware.MiddlewareEvent) {
	p.panics++
	panic("boom")
}

// countMW 记录自己收到过多少次事件。
type countMW struct {
	netMiddleware.MiddlewareBase
	got int
}

func (c *countMW) OnEvent(netMiddleware.MiddlewareEvent) { c.got++ }

func newHead() (netMiddleware.Middleware, *countMW) {
	first := netMiddlewareCommon.NewMiddlewareFirst(func([]byte) error { return nil })
	tail := &countMW{}
	first.SetNext(tail)
	tail.SetPre(first)
	return first, tail
}

// 回归 CRASH-03：`FireEvent` 把 `firingEvent` 置 true 后调用 OnEvent，
// 而 OnEvent 可能 panic（type1 的状态守卫就是显式 panic）。panic 展开时
// firingEvent 永远停在 true，且执行它的 goroutine 已经没了 —— 此后这条链上的
// 所有事件（包括 OnDisconnect）只入队、**永不派发**，中间件的握手状态与
// 密钥字段也不做任何清理。
func TestFireEvent_PanicDoesNotWedgeChainForever(t *testing.T) {
	// 在同一进程内驱动完整序列：panic 由外层 recover 隔离，链对象保持存活，
	// 这样才能观察"panic 之后链还能不能用"。
	first, tail := newHead()
	bad := &panicMW{}
	first.SetNext(bad)
	bad.SetPre(first)

	func() {
		defer func() { _ = recover() }()
		first.FireEvent(netMiddleware.MiddlewareEventOnConnect)
	}()
	testutil.Equal(t, 1, bad.panics, "OnEvent 应确实触发过一次 panic")

	// 关键断言：把下游换成正常中间件后，后续事件必须还能派发出去。
	// 未修复时 firingEvent 卡在 true，这里永远收不到事件。
	first.SetNext(tail)
	tail.SetPre(first)
	first.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
	testutil.Equal(t, 1, tail.got,
		"CRASH-03 未修复：OnEvent panic 后 firingEvent 永久为 true，本链再不平息派发任何事件")

	// 重入排队语义必须仍然成立：派发中再次 FireEvent 应排队而非递归。
	deep := &reentrantMW{}
	first.SetNext(deep)
	deep.SetPre(first)
	deep.fire = func(e netMiddleware.MiddlewareEvent) {
		first.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
	}
	first.FireEvent(netMiddleware.MiddlewareEventOnConnect)
	testutil.True(t, deep.calls >= 2, "重入事件应被排队并继续派发，calls", deep.calls)
}

type reentrantMW struct {
	netMiddleware.MiddlewareBase
	calls int
	fire  func(netMiddleware.MiddlewareEvent)
}

func (r *reentrantMW) OnEvent(e netMiddleware.MiddlewareEvent) {
	r.calls++
	if r.calls < 5 && r.fire != nil {
		f := r.fire
		r.fire = nil
		f(e)
	}
}

// 正常路径：无 panic 时事件按队列顺序全部派发完毕，且不留残余。
func TestFireEvent_DrainsReentrantQueue(t *testing.T) {
	first, tail := newHead()
	first.FireEvent(netMiddleware.MiddlewareEventOnConnect)
	first.FireEvent(netMiddleware.MiddlewareEventOnReady)
	first.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
	testutil.Equal(t, 3, tail.got)

	// 事件不应在链上无界堆积
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, 3, tail.got, "派发结束后不应继续增加")
}
