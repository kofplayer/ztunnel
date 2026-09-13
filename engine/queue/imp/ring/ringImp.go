package queueImpRing

// QQueue 是 queueDef.Queue 的环形缓冲实现。
type QQueue struct {
	queue *Queue[interface{}]
	// limit 是允许的待处理条目上限；<=0 表示不限长。
	limit int
}

// DefaultSendQueueLimit 是每条发送队列允许的待处理帧数上限。
//
// 为什么需要（报告 M-17）：发送队列原本只增不减——`RingBuffer.Push` 满了就把
// 容量翻倍，慢消费者 + 快生产者可以让它一直长下去，最终在某次 `make` 分配失败时
// **在调用 SendMessage 的那个 goroutine 里** panic；那个 goroutine 常常是业务
// handler 所在的 goroutine，外面没有 recoverPanic 兜底，进程直接死。
// 有了上限，积压退化成一个可判定的 ErrFull，由上层决定断开还是丢弃。
//
// 64K 帧的选择：数据通道单帧最大 4096B（读缓冲大小），因此 64K 帧约 256MB 是
// 一条隧道的上限而非全局上限；正常代理场景（消费端跟得上）永远碰不到，只有
// 对端真正停摆时才会触发——那正是我们想让它断开而不是把机器拖垮的情况。
const DefaultSendQueueLimit = 64 * 1024

// Init(baseBufferCount int) error
// Close() error
// IsClose() bool
// Enqueue(data interface{}) error
// Dequeue() (interface{}, bool)

func (q *QQueue) Init(baseBufferCount int) error {
	q.queue = NewQueue[interface{}](baseBufferCount)
	q.limit = DefaultSendQueueLimit
	return nil
}

// SetLimit 调整待处理条目上限（<=0 表示不限长）。供测试与特殊场景使用。
func (q *QQueue) SetLimit(n int) { q.limit = n }

func (q *QQueue) Close() error {
	q.queue.Close()
	return nil
}

func (q *QQueue) IsClose() bool {
	return q.queue.IsClosed()
}

// Enqueue 把 data 放入队列。
//
// 判定与入队在同一次加锁里完成（见 SendLimited），不存在"先看长度再入队"的
// 检查-使用竞态。返回的错误用 errors.Is 区分：
//   - ErrClosed：队列已关闭（连接已断开）
//   - ErrFull：积压达到上限（背压）
//
// 这里不再是每次 `errors.New("closed")`：那样既无法区分两种失败，也白分配对象
// （报告 P-04）。
func (q *QQueue) Enqueue(data interface{}) error {
	return q.queue.SendLimited(data, q.limit)
}

func (q *QQueue) Dequeue() (interface{}, bool) {
	return q.queue.Receive()
}
