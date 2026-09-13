package queue

import (
	"testing"

	"ztunnel/testutil"
)

// 回归 L-5：NewQueue 原先对非法的 buffLen 毫无防护——`Init` 永不返回 error，
// 那个 `panic(err)` 是死代码；真正会炸的是 NewRingBuffer 对 `size <= 0` 的 panic，
// 而它发生在**调用方**（例如 newConn）里，调用方往往没有 recover。
func TestNewQueue_NonPositiveLengthIsClamped(t *testing.T) {
	for _, n := range []int{0, -1, -1024} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("L-5 未修复：NewQueue(%d) 在调用方 panic: %v", n, r)
				}
			}()
			q := NewQueue(n)
			testutil.True(t, q != nil, "NewQueue 不得返回 nil; buffLen", n)

			// 钳制后必须是可用的
			testutil.NoError(t, q.Enqueue("a"))
			testutil.NoError(t, q.Enqueue("b"))
			v, ok := q.Dequeue()
			testutil.True(t, ok)
			testutil.Equal(t, "a", v.(string))
			v, ok = q.Dequeue()
			testutil.True(t, ok)
			testutil.Equal(t, "b", v.(string))
			testutil.NoError(t, q.Close())
		}()
	}
}

// 正常容量不受钳制逻辑影响。
func TestNewQueue_PositiveLengthStillWorks(t *testing.T) {
	q := NewQueue(DefaultInitLen)
	testutil.NoError(t, q.Enqueue(1))
	v, ok := q.Dequeue()
	testutil.True(t, ok)
	testutil.Equal(t, 1, v.(int))

	if DefaultInitLen <= 0 {
		t.Fatalf("默认初始容量应为正数, got %d", DefaultInitLen)
	}
}
