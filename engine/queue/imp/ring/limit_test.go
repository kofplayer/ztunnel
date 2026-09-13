package queueImpRing

import (
	"errors"
	"sync"
	"testing"
)

// 回归 M-17：发送队列原本只增不减——`RingBuffer.Push` 满了就把容量翻倍，
// 慢消费者 + 快生产者能让它无限增长，最后在某次 `make` 分配失败时**在调用方
// goroutine 里 panic**（那往往是业务 handler 所在的 goroutine，外面没有
// recoverPanic 兜底），进程直接死。上限把积压变成一个可判定的错误。

func TestQueue_SendLimited_RejectsOverLimit(t *testing.T) {
	q := NewQueue[int](2)

	for i := 0; i < 10; i++ {
		if err := q.SendLimited(i, 10); err != nil {
			t.Fatalf("上限内第 %d 次入队失败: %v", i, err)
		}
	}
	if q.Len() != 10 {
		t.Fatalf("应有 10 个待处理条目, got %d", q.Len())
	}
	err := q.SendLimited(99, 10)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("超限应返回 ErrFull, got %v", err)
	}
	if q.Len() != 10 {
		t.Fatalf("被拒绝的入队不得改变队列长度, got %d", q.Len())
	}

	// 腾出空间后必须能继续入队
	if _, ok := q.TryReceive(); !ok {
		t.Fatal("TryReceive 应成功")
	}
	if err := q.SendLimited(99, 10); err != nil {
		t.Fatalf("腾出空间后应可再入队: %v", err)
	}
}

// maxLen<=0 表示不限长，保持旧语义。
func TestQueue_SendLimited_UnlimitedWhenNonPositive(t *testing.T) {
	q := NewQueue[int](2)
	for i := 0; i < 5000; i++ {
		if err := q.SendLimited(i, 0); err != nil {
			t.Fatalf("不限长模式第 %d 次入队失败: %v", i, err)
		}
	}
	if q.Len() != 5000 {
		t.Fatalf("got %d", q.Len())
	}
}

// ErrClosed 与 ErrFull 必须是两个可区分的哨兵：前者代表"连接已断"，
// 后者代表"背压超限"，上层的处置方式完全不同。
func TestQueue_ErrClosedAndErrFullAreDistinguishable(t *testing.T) {
	q := NewQueue[int](4)
	if err := q.SendLimited(1, 1); err != nil {
		t.Fatalf("首次入队应成功: %v", err)
	}
	if err := q.SendLimited(2, 1); !errors.Is(err, ErrFull) {
		t.Fatalf("应返回 ErrFull, got %v", err)
	}
	q.Close()
	// 已关闭优先于超限：连接都断了，讨论上限没有意义
	if err := q.SendLimited(3, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后应返回 ErrClosed, got %v", err)
	}
	if errors.Is(ErrFull, ErrClosed) || errors.Is(ErrClosed, ErrFull) {
		t.Fatal("两个哨兵错误必须互不等价")
	}
}

// 检查-使用竞态：判定与入队必须在同一次加锁内完成，否则并发下能冲过上限。
func TestQueue_SendLimit_IsAtomicUnderConcurrency(t *testing.T) {
	const (
		limit      = 100
		goroutines = 16
		perG       = 200
	)
	q := NewQueue[int](8)

	var wg sync.WaitGroup
	var overflows int
	var mu sync.Mutex
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := q.SendLimited(g*perG+i, limit); err != nil {
					mu.Lock()
					overflows++
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()

	if q.Len() > limit {
		t.Fatalf("上限被并发冲过：Len=%d, limit=%d（判定与入队必须同锁完成）", q.Len(), limit)
	}
	if q.Len()+overflows != goroutines*perG {
		t.Fatalf("账目不平：入队 %d + 拒绝 %d != 尝试 %d",
			q.Len(), overflows, goroutines*perG)
	}
}

// QQueue 适配层：默认带上限，且错误可用 errors.Is 判定（顺带修掉 P-04
// 每次调用都 errors.New("closed") 的问题）。
func TestQQueue_UsesSentinelErrorsAndLimit(t *testing.T) {
	var q QQueue
	if err := q.Init(4); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if q.limit != DefaultSendQueueLimit {
		t.Fatalf("默认上限应为 %d, got %d", DefaultSendQueueLimit, q.limit)
	}

	q.SetLimit(3)
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("上限内 Enqueue 失败: %v", err)
		}
	}
	if err := q.Enqueue(99); !errors.Is(err, ErrFull) {
		t.Fatalf("超限应返回 ErrFull, got %v", err)
	}

	q.SetLimit(0) // 不限长
	if err := q.Enqueue(99); err != nil {
		t.Fatalf("SetLimit(0) 后应不再受限: %v", err)
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := q.Enqueue(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后应返回 ErrClosed, got %v", err)
	}
}
