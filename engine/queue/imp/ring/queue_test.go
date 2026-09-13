package queueImpRing

import (
	"slices"
	"sync"
	"testing"
	"time"

	"ztunnel/testutil"
)

// ---------------------------------------------------------------------------
// RingBuffer
// ---------------------------------------------------------------------------

func eqInts(t *testing.T, want, got []int, what string) {
	t.Helper()
	testutil.True(t, slices.Equal(want, got), what, "want", want, "got", got)
}

func TestRingBuffer_PushPopOrdering(t *testing.T) {
	rb := NewRingBuffer[int](2)

	// 小容量下的反复入出队，会迫使 head/tail 多次绕回缓冲区末端
	got := make([]int, 0, 50)
	for i := 0; i < 20; i++ {
		rb.Push(i)
	}
	for i := 0; i < 8; i++ {
		v, ok := rb.Pop()
		testutil.True(t, ok)
		got = append(got, v)
	}
	for i := 20; i < 40; i++ {
		rb.Push(i)
	}
	for !rb.IsEmpty() {
		v, _ := rb.Pop()
		got = append(got, v)
	}

	want := make([]int, 0, 40)
	for i := 0; i < 40; i++ {
		want = append(want, i)
	}
	testutil.Equal(t, 40, len(got), "取出的元素数不符")
	eqInts(t, want, got, "FIFO 顺序错乱")
}

func TestRingBuffer_GrowsOnPush(t *testing.T) {
	rb := NewRingBuffer[int](1)
	testutil.Equal(t, 1, rb.Size())
	for i := 0; i < 1000; i++ {
		rb.Push(i)
	}
	testutil.Equal(t, 1000, rb.Count())
	testutil.True(t, rb.Size() >= 1000, "容量应已扩容到不小于元素数，实际", rb.Size())
}

func TestRingBuffer_PopOnEmpty(t *testing.T) {
	rb := NewRingBuffer[int](4)
	v, ok := rb.Pop()
	testutil.True(t, !ok, "空缓冲区 Pop 应返回 false")
	testutil.Equal(t, 0, v)
	testutil.True(t, rb.IsFull() == false, "空缓冲区不应为满")
}

func TestRingBuffer_PushBatch(t *testing.T) {
	rb := NewRingBuffer[int](2)

	// 空批次是 no-op
	rb.PushBatch([]int{})
	testutil.Equal(t, 0, rb.Count())

	// 一次塞入远超当前容量的数量：应连续翻倍扩容而非丢失数据
	batch := make([]int, 0, 300)
	for i := 0; i < 300; i++ {
		batch = append(batch, i)
	}
	rb.PushBatch(batch)
	testutil.Equal(t, 300, rb.Count())
	eqInts(t, batch, rb.GetAll(), "批量写入后内容/顺序不符")

	// 已绕回头部之后的批量写入（head != 0 的分支）
	rb.PopAll()
	for i := 0; i < 10; i++ {
		rb.Push(i)
	}
	for i := 0; i < 5; i++ {
		rb.Pop()
	}
	rb.PushBatch([]int{100, 101, 102})
	eqInts(t, []int{5, 6, 7, 8, 9, 100, 101, 102}, rb.GetAll(),
		"绕回状态下批量写入后仍应为逻辑顺序")
}

func TestRingBuffer_PopAll(t *testing.T) {
	rb := NewRingBuffer[int](4)

	// 空缓冲区返回空切片而非 nil
	empty := rb.PopAll()
	testutil.Equal(t, 0, len(empty))
	testutil.True(t, empty != nil, "PopAll 空时应返回空切片而非 nil")

	for i := 1; i <= 3; i++ {
		rb.Push(i)
	}
	eqInts(t, []int{1, 2, 3}, rb.PopAll(), "PopAll 顺序不符")
	testutil.Equal(t, 0, rb.Count())
	testutil.True(t, rb.IsEmpty())

	// 跨尾部分两段的情况：先填满再弹出几枚，使 head>tail
	for i := 1; i <= 4; i++ {
		rb.Push(i)
	}
	rb.Pop()
	rb.Pop()
	rb.Push(5)
	rb.Push(6)
	eqInts(t, []int{3, 4, 5, 6}, rb.PopAll(), "回绕后 PopAll 必须按逻辑顺序返回")
}

// PopAll 必须切断对被弹出元素的引用，否则滞留的指针会拖住整个底层数组。
func TestRingBuffer_PopAllClearsReferences(t *testing.T) {
	type holder struct{ payload [1 << 12]byte }
	rb := NewRingBuffer[*holder](8)
	for i := 0; i < 4; i++ {
		rb.Push(&holder{})
	}
	rb.PopAll()
	for i := 0; i < 4; i++ {
		testutil.True(t, rb.buffer[i] == nil, "槽位未清零，仍引用旧对象; index", i)
	}
}

func TestRingBuffer_Peek(t *testing.T) {
	rb := NewRingBuffer[int](4)
	v, ok := rb.Peek()
	testutil.True(t, !ok, "空缓冲区 Peek 应返回 false")
	testutil.Equal(t, 0, v)

	rb.Push(11)
	rb.Push(22)
	v, ok = rb.Peek()
	testutil.True(t, ok)
	testutil.Equal(t, 11, v)
	testutil.Equal(t, 2, rb.Count(), "Peek 不应移除元素")
}

func TestRingBuffer_GetAllAndCount(t *testing.T) {
	rb := NewRingBuffer[int](4)
	testutil.Equal(t, 0, len(rb.GetAll()), "空缓冲区 GetAll 应返回空切片")

	for i := 1; i <= 3; i++ {
		rb.Push(i)
	}
	eqInts(t, []int{1, 2, 3}, rb.GetAll(), "GetAll 内容不符")
	testutil.Equal(t, 3, rb.Count())
	// GetAll 是非破坏性的
	testutil.Equal(t, 3, rb.Count(), "GetAll 不应移除元素")
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer[int](4)
	for i := 1; i <= 3; i++ {
		rb.Push(i)
	}
	rb.Clear()
	testutil.Equal(t, 0, rb.Count())
	testutil.True(t, rb.IsEmpty())
	testutil.Equal(t, 0, len(rb.GetAll()))

	// Clear 之后必须能继续正常写入并读回
	rb.Push(7)
	v, ok := rb.Pop()
	testutil.True(t, ok)
	testutil.Equal(t, 7, v)
}

// 表征测试（characterization）：Clear 只重置游标、**不清零槽位**。
// 这是已知缺陷 L-4（滞留的 []byte 会拖住底层数组），本用例钉住现状；
// 若将来修复为零值，需同步更新此断言。
func TestRingBuffer_ClearKeepsStaleSlots(t *testing.T) {
	type holder struct{ v int }
	rb := NewRingBuffer[*holder](4)
	rb.Push(&holder{1})
	rb.Push(&holder{2})
	rb.Clear()
	testutil.True(t, rb.buffer[0] != nil,
		"当前实现 Clear 不清零槽位（报告 L-4）；若已修复请更新本用例")
}

// ---------------------------------------------------------------------------
// Queue[T]
// ---------------------------------------------------------------------------

func TestQueue_SendReceive(t *testing.T) {
	q := NewQueue[int](2)
	testutil.Equal(t, 0, q.Len())
	testutil.True(t, !q.IsClosed())

	testutil.True(t, q.Send(1))
	testutil.True(t, q.Send(2))
	testutil.Equal(t, 2, q.Len())

	v, ok := q.Receive()
	testutil.True(t, ok)
	testutil.Equal(t, 1, v)
}

func TestQueue_TryReceive(t *testing.T) {
	q := NewQueue[int](2)

	v, ok := q.TryReceive()
	testutil.True(t, !ok, "空队列 TryReceive 应返回 false")
	testutil.Equal(t, 0, v)

	q.Send(42)
	v, ok = q.TryReceive()
	testutil.True(t, ok)
	testutil.Equal(t, 42, v)

	_, ok = q.TryReceive()
	testutil.True(t, !ok)
}

func TestQueue_BatchSendAndReceiveBatch(t *testing.T) {
	q := NewQueue[int](2)

	// 空批次：仍算成功，且不唤醒接收者
	testutil.True(t, q.BatchSend([]int{}))
	testutil.Equal(t, 0, q.Len())

	testutil.True(t, q.BatchSend([]int{1, 2, 3, 4}))
	testutil.Equal(t, 4, q.Len())

	got, ok := q.ReceiveBatch()
	testutil.True(t, ok)
	eqInts(t, []int{1, 2, 3, 4}, got, "ReceiveBatch 内容不符")
	testutil.Equal(t, 0, q.Len(), "ReceiveBatch 应取空")

	// 单元素批次走 Signal 分支
	testutil.True(t, q.BatchSend([]int{9}))
	got, ok = q.ReceiveBatch()
	testutil.True(t, ok)
	eqInts(t, []int{9}, got, "单元素批量接收不符")
}

func TestQueue_TryReceiveBatch(t *testing.T) {
	q := NewQueue[int](4)

	// 未关闭且为空：返回 (nil, true) —— 与"已关闭且为空"必须能区分
	got, ok := q.TryReceiveBatch()
	testutil.True(t, ok, "队列仍打开时 TryReceiveBatch 不应报关闭")
	testutil.Equal(t, 0, len(got))

	q.BatchSend([]int{1, 2, 3})
	got, ok = q.TryReceiveBatch()
	testutil.True(t, ok)
	eqInts(t, []int{1, 2, 3}, got, "TryReceiveBatch 内容不符")

	q.Close()
	got, ok = q.TryReceiveBatch()
	testutil.True(t, !ok, "已关闭且无数据时应返回 false")
	testutil.Equal(t, 0, len(got))
}

// 队列关闭后必须仍能排空存量数据，这是 senderRun 语义的前提。
func TestQueue_CloseStillDrains(t *testing.T) {
	q := NewQueue[int](4)
	q.BatchSend([]int{1, 2, 3})
	q.Close()

	testutil.True(t, q.IsClosed())
	for i := 1; i <= 3; i++ {
		v, ok := q.Receive()
		testutil.True(t, ok, "关闭后应仍能取出残留数据")
		testutil.Equal(t, i, v)
	}
	_, ok := q.Receive()
	testutil.True(t, !ok, "排空后应返回 false")

	// 关闭后写入一律失败
	testutil.True(t, !q.Send(9))
	testutil.True(t, !q.BatchSend([]int{9}))
}

// Close 必须幂等：重复 close(notifyClose) 会 panic。
func TestQueue_CloseIsIdempotent(t *testing.T) {
	q := NewQueue[int](2)
	q.Send(1)
	q.Close()
	q.Close()
	q.Close()
	testutil.True(t, q.IsClosed())

	select {
	case <-q.NotifyClose():
	default:
		t.Fatal("NotifyClose 通道应在 Close 后被关闭")
	}
}

// 阻塞中的 Receive 必须被 Close 唤醒，否则发送方退出后接收方永久挂起。
func TestQueue_CloseWakesBlockedReceiver(t *testing.T) {
	q := NewQueue[int](2)

	result := make(chan bool, 1)
	go func() {
		_, ok := q.Receive()
		result <- ok
	}()

	time.Sleep(100 * time.Millisecond)
	q.Close()

	select {
	case ok := <-result:
		testutil.True(t, !ok, "被 Close 唤醒的 Receive 应返回 false")
	case <-time.After(3 * time.Second):
		t.Fatal("Close 未能唤醒阻塞中的 Receive")
	}
}

// 阻塞中的 ReceiveBatch 同样必须被 Close 唤醒。
func TestQueue_CloseWakesBlockedBatchReceiver(t *testing.T) {
	q := NewQueue[int](2)

	got := make(chan bool, 1)
	go func() {
		_, ok := q.ReceiveBatch()
		got <- ok
	}()

	time.Sleep(100 * time.Millisecond)
	q.Close()

	select {
	case ok := <-got:
		testutil.True(t, !ok)
	case <-time.After(3 * time.Second):
		t.Fatal("Close 未能唤醒阻塞中的 ReceiveBatch")
	}
}

// 多生产者 + 多消费者下不得丢失或重复元素。
func TestQueue_ConcurrentProducersConsumers(t *testing.T) {
	const producers, perP, consumers = 6, 200, 4
	q := NewQueue[int](8)

	var pw sync.WaitGroup
	for p := 0; p < producers; p++ {
		pw.Add(1)
		go func(p int) {
			defer pw.Done()
			for i := 0; i < perP; i++ {
				q.Send(p*perP + i)
			}
		}(p)
	}

	var cw sync.WaitGroup
	out := make(chan int, producers*perP)
	for c := 0; c < consumers; c++ {
		cw.Add(1)
		go func() {
			defer cw.Done()
			for {
				v, ok := q.Receive()
				if !ok {
					return
				}
				out <- v
			}
		}()
	}

	pw.Wait()
	// 全部入队后关闭，消费者排空存量即退出
	q.Close()
	cw.Wait()
	close(out)

	seen := make(map[int]bool, producers*perP)
	for v := range out {
		testutil.True(t, !seen[v], "元素被重复投递; value", v)
		seen[v] = true
	}
	testutil.Equal(t, producers*perP, len(seen), "元素丢失")
}

// 批量发送必须唤醒**所有**等待者；只 Signal 一个会造成其余人永久挂起。
func TestQueue_BatchSendWakesAllWaiters(t *testing.T) {
	q := NewQueue[int](2)
	const waiters = 8
	done := make(chan int, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			v, ok := q.Receive()
			if ok {
				done <- v
			}
		}()
	}
	time.Sleep(150 * time.Millisecond)
	q.BatchSend([]int{10, 11, 12, 13, 14, 15, 16, 17})

	got := make(map[int]bool)
	deadline := time.After(5 * time.Second)
	for len(got) < waiters {
		select {
		case v := <-done:
			got[v] = true
		case <-deadline:
			t.Fatalf("只有 %d/%d 个等待者被唤醒", len(got), waiters)
		}
	}
}

// ---------------------------------------------------------------------------
// QQueue（queueDef.Queue 的实现）
// ---------------------------------------------------------------------------

func TestQQueue_Adapter(t *testing.T) {
	var q QQueue
	testutil.NoError(t, q.Init(4))
	testutil.True(t, !q.IsClose())

	testutil.NoError(t, q.Enqueue([]byte("hello")))
	testutil.Equal(t, 1, q.queue.Len())

	v, ok := q.Dequeue()
	testutil.True(t, ok)
	testutil.BytesEqual(t, []byte("hello"), v.([]byte))

	testutil.NoError(t, q.Close())
	testutil.True(t, q.IsClose(), "IsClose 应反映关闭状态")
	testutil.Error(t, q.Enqueue("x"), "关闭后 Enqueue 必须报错")
}
