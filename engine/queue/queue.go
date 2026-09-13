package queue

import (
	queueDef "ztunnel/engine/queue/def"
	queueImpRing "ztunnel/engine/queue/imp/ring"
)

// DefaultInitLen 是发送队列的默认初始容量。
const DefaultInitLen = 32

// NewQueue 创建一条阻塞队列。
//
// 修复 L-5：原先 `Init` 永不返回 error，这里的 `panic(err)` 是死代码；真正的
// 崩溃点在 NewRingBuffer 对 `size <= 0` 的 panic —— 它会**在调用方**（例如
// newConn）里炸掉，而调用方往往没有 recover。现在把非正数钳制为默认容量，
// 入口对非法参数鲁棒。
func NewQueue(buffLen int) queueDef.Queue {
	if buffLen <= 0 {
		buffLen = DefaultInitLen
	}
	r := new(queueImpRing.QQueue)
	if err := r.Init(buffLen); err != nil {
		// 理论上不可达（Init 目前恒返回 nil）。保留快速失败：返回 nil 队列只会
		// 把它变成后续更难定位的 nil 解引用（L-5 的教训不应换个形式重现）。
		panic(err)
	}
	return r
}
