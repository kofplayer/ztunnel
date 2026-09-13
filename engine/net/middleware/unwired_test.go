package netMiddleware_test

import (
	"testing"

	netMiddleware "ztunnel/engine/net/middleware"
)

// 回归 L-1：`MiddlewareBase.ReceiveData` / `SendData` 原先直接解引用 m.next / m.pre，
// 而同一文件的 `OnEvent` 早就对 next 判了空——三处标准不一致。链一旦配错
// （例如把 fullData 放到链尾，或 CreateMiddlewareFunc 工厂返回 nil 造成接线残缺），
// 这里就是 nil 接口调用 panic。现在统一返回明确 error。

// stubMW 只嵌入 MiddlewareBase，不接任何上下游。
type stubMW struct {
	netMiddleware.MiddlewareBase
}

func TestMiddlewareBase_UnwiredReceiveReturnsErrorNotPanic(t *testing.T) {
	m := &stubMW{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("L-1 未修复：链尾未接线时 ReceiveData panic: %v", r)
		}
	}()
	if err := m.ReceiveData([]byte{1, 2, 3}); err == nil {
		t.Fatal("next 为 nil 时应返回 error")
	}
}

func TestMiddlewareBase_UnwiredSendReturnsErrorNotPanic(t *testing.T) {
	m := &stubMW{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("L-1 未修复：链头未接线时 SendData panic: %v", r)
		}
	}()
	if err := m.SendData([]byte{1, 2, 3}); err == nil {
		t.Fatal("pre 为 nil 时应返回 error")
	}
}

// 接线正确时不得被守卫误伤。
func TestMiddlewareBase_WiredPathStillWorks(t *testing.T) {
	head := &stubMW{}
	tail := &stubMW{}
	head.SetNext(tail)
	tail.SetPre(head)

	// tail 的 next 为 nil => 传到 tail 后返回 error，但 head->tail 这一跳必须走通
	if err := head.ReceiveData([]byte{7}); err == nil {
		t.Fatal("链尾仍应报错（说明 tail.next 为 nil）")
	}
	if err := tail.SendData([]byte{7}); err == nil {
		t.Fatal("链头仍应报错（说明 tail.pre 指向 head、head.pre 为 nil）")
	}
	// head 的 pre 为 nil => 直接报错，不 panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("接线中间节点时不应 panic: %v", r)
		}
	}()
	_ = head.SendData([]byte{7})
}
