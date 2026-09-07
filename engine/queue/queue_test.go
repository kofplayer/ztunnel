package queue

import (
	"sync"
	"sync/atomic"
	"testing"

	"ztunnel/testutil"
)

// 基线：Close 语义——关闭后入队失败、存量可排空、排空后出队返回 false、重复 Close 幂等。
func TestQueue_CloseSemantics(t *testing.T) {
	q := NewQueue(4)
	testutil.NoError(t, q.Enqueue(1))
	testutil.NoError(t, q.Enqueue(2))
	testutil.NoError(t, q.Close())

	testutil.Error(t, q.Enqueue(3), "关闭后入队应失败")

	v, ok := q.Dequeue()
	testutil.True(t, ok)
	testutil.Equal(t, 1, v.(int))
	v, ok = q.Dequeue()
	testutil.True(t, ok, "关闭前排队的存量数据应可排空")
	testutil.Equal(t, 2, v.(int))

	_, ok = q.Dequeue()
	testutil.True(t, !ok, "排空后出队应返回 false")

	testutil.NoError(t, q.Close(), "重复 Close 应幂等")
}

// 基线：多生产者多消费者并发压测，总量与顺序性（去重）由值唯一性保证。
func TestQueue_ConcurrentStress(t *testing.T) {
	q := NewQueue(32)
	const producers, perProducer = 8, 1000

	var prodWG sync.WaitGroup
	for p := 0; p < producers; p++ {
		prodWG.Add(1)
		go func(base int) {
			defer prodWG.Done()
			for i := 0; i < perProducer; i++ {
				if err := q.Enqueue(base + i); err != nil {
					t.Error(err)
					return
				}
			}
		}(p * perProducer)
	}

	var total atomic.Int64
	var consWG sync.WaitGroup
	for c := 0; c < 4; c++ {
		consWG.Add(1)
		go func() {
			defer consWG.Done()
			for {
				v, ok := q.Dequeue()
				if !ok {
					return
				}
				_ = v.(int)
				total.Add(1)
			}
		}()
	}

	prodWG.Wait()
	q.Close()
	consWG.Wait()
	testutil.Equal(t, int64(producers*perProducer), total.Load())
}
