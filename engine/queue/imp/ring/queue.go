package queueImpRing

import (
	"errors"
	"sync"
)

// Queue 是一个带条件变量的阻塞队列。
//
// 它自身仍可无限扩容（Push → RingBuffer 翻倍）；**经 QQueue 使用时默认受长度
// 上限约束**（SendLimited + DefaultSendQueueLimit），见报告 M-17。
type Queue[T any] struct {
	buffer      *RingBuffer[T] // 用于存储数据的链表
	mutex       sync.Mutex     // 互斥锁，保证并发安全
	notEmpty    *sync.Cond     // 条件变量，用于通知有数据可读
	notifyClose chan struct{}  // 用于通知channel已关闭
	closed      bool           // 标记channel是否已关闭
	zero        T
}

// New 创建一个新的无限缓冲区channel
func NewQueue[T any](baseBufferCount int) *Queue[T] {
	ch := &Queue[T]{
		buffer:      NewRingBuffer[T](baseBufferCount), // 初始环形缓冲区，自动扩容
		notifyClose: make(chan struct{}),
		closed:      false,
	}
	ch.notEmpty = sync.NewCond(&ch.mutex)
	return ch
}

// ErrClosed 表示队列已关闭（连接已断开）。包级哨兵，供 errors.Is 判定。
var ErrClosed = errors.New("queue: closed")

// ErrFull 表示队列长度已达上限，拒绝继续入队（背压信号）。
//
// 修复 M-17：发送队列原本是**只增不减**的——`RingBuffer.Push` 满了就
// `size*2` 无限翻倍扩容，慢消费者 + 快生产者的场景下内存可以被无界拉高，
// 直到 `make` 分配失败在**调用方 goroutine** 里 panic（不是有 recover 兜底的
// 收发循环，而可能是业务 goroutine）。有了上限，积压变成一条可判定的错误。
var ErrFull = errors.New("queue: length limit reached")

// SendLimited 在**同一把锁内**完成"已关闭 / 超限 / 入队"三件事。
//
// 不提供"先 Len() 再 Send()"的写法：那两步之间有竞态，突发并发下会把上限
// 冲过去。maxLen <= 0 表示不限长（保持旧语义）。
func (ch *Queue[T]) SendLimited(value T, maxLen int) error {
	ch.mutex.Lock()
	if ch.closed {
		ch.mutex.Unlock()
		return ErrClosed
	}
	if maxLen > 0 && ch.buffer.Count() >= maxLen {
		ch.mutex.Unlock()
		return ErrFull
	}
	ch.buffer.Push(value)
	ch.mutex.Unlock()
	ch.notEmpty.Signal()
	return nil
}

// Send 向channel发送数据
// 如果channel已关闭，则返回false，否则返回true
func (ch *Queue[T]) Send(value T) bool {
	ch.mutex.Lock()
	// defer ch.mutex.Unlock()

	if ch.closed {
		ch.mutex.Unlock()
		return false
	}

	ch.buffer.Push(value)
	ch.mutex.Unlock()
	ch.notEmpty.Signal() // 通知可能在等待的接收者
	return true
}

// BatchSend 批量发送数据到队列
// 如果队列已关闭，则返回false，否则全部发送并返回true
func (ch *Queue[T]) BatchSend(values []T) bool {
	ch.mutex.Lock()
	// defer ch.mutex.Unlock()

	if ch.closed {
		ch.mutex.Unlock()
		return false
	}
	ch.buffer.PushBatch(values)
	ch.mutex.Unlock()
	l := len(values)
	if l > 1 {
		ch.notEmpty.Broadcast() // 通知所有等待的接收者
	} else if l > 0 {
		ch.notEmpty.Signal() // 通知可能在等待的接收者
	}
	return true
}

// Receive 从channel接收数据
// 如果channel已关闭且没有数据，则返回零值和false，否则返回数据和true
func (ch *Queue[T]) Receive() (T, bool) {
	ch.mutex.Lock()

	// 等待直到有数据或channel关闭
	for ch.buffer.Count() == 0 && !ch.closed {
		// 释放锁并等待通知
		ch.notEmpty.Wait()
	}

	// 如果buffer为空且channel已关闭，返回零值
	if ch.buffer.Count() == 0 && ch.closed {
		ch.mutex.Unlock()
		return ch.zero, false
	}

	// 取出数据
	value, _ := ch.buffer.Pop()
	ch.mutex.Unlock()
	return value, true
}

// ReceiveBatch 一次性获取队列中所有元素（如果队列为空且未关闭则阻塞等待）
// 返回获取到的切片，如果队列已关闭且无数据则返回空切片和false
func (ch *Queue[T]) ReceiveBatch() ([]T, bool) {
	ch.mutex.Lock()
	for ch.buffer.Count() == 0 && !ch.closed {
		ch.notEmpty.Wait()
	}
	if ch.buffer.Count() == 0 && ch.closed {
		ch.mutex.Unlock()
		return nil, false
	}
	result := ch.buffer.PopAll()
	ch.mutex.Unlock()
	return result, true
}

// TryReceive 尝试从channel接收数据，但不会阻塞
// 如果有数据可读，则返回数据和true，否则返回零值和false
func (ch *Queue[T]) TryReceive() (T, bool) {
	ch.mutex.Lock()

	if ch.buffer.Count() == 0 {
		ch.mutex.Unlock()
		return ch.zero, false
	}

	value, _ := ch.buffer.Pop()
	ch.mutex.Unlock()
	return value, true
}

// TryReceiveBatch 尝试批量接收数据，不会阻塞
// 返回实际接收到的数据切片（可能为空），如果队列已关闭且无数据则返回空切片和false
func (ch *Queue[T]) TryReceiveBatch() ([]T, bool) {
	ch.mutex.Lock()

	n := ch.buffer.Count()
	if n == 0 {
		if ch.closed {
			ch.mutex.Unlock()
			return nil, false
		}
		ch.mutex.Unlock()
		return nil, true
	}
	result := ch.buffer.PopAll()
	ch.mutex.Unlock()
	return result, true
}

// Close 关闭channel
func (ch *Queue[T]) Close() {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()

	if !ch.closed {
		ch.closed = true
		close(ch.notifyClose)
		ch.notEmpty.Broadcast() // 通知所有等待的接收者
	}
}

// Len 返回当前channel中的元素数量
func (ch *Queue[T]) Len() int {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()
	return ch.buffer.Count()
}

// IsClosed 返回channel是否已关闭
func (ch *Queue[T]) IsClosed() bool {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()
	return ch.closed
}

// NotifyClose 返回一个只读channel，当UnboundedChannel关闭时会关闭
func (ch *Queue[T]) NotifyClose() <-chan struct{} {
	return ch.notifyClose
}
