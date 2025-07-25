package queue

import (
	queueDef "ztunnel/engine/queue/def"
	queueImpRing "ztunnel/engine/queue/imp/ring"
)

func NewQueue(buffLen int) queueDef.Queue {
	r := new(queueImpRing.QQueue)
	err := r.Init(buffLen)
	if err != nil {
		panic(err)
	}
	return r
}
