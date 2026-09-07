package testutil

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	netMiddleware "ztunnel/engine/net/middleware"
	netMiddlewareCommon "ztunnel/engine/net/middleware/common"
)

// BuildChain 按 netClient.Connect 的生产逻辑拼装中间件链：
// first(send) → mws... → last(onReceive, onReady)。
func BuildChain(send func([]byte) error, creates []netMiddleware.CreateMiddlewareFunc,
	onReceive func([]byte) error, onReady func()) (first, last netMiddleware.Middleware) {
	first = netMiddlewareCommon.NewMiddlewareFirst(send)
	current := first
	for _, f := range creates {
		m := f()
		m.SetPre(current)
		current.SetNext(m)
		current = m
	}
	last = netMiddlewareCommon.NewMiddlewareLast(onReceive, onReady)
	last.SetPre(current)
	current.SetNext(last)
	return first, last
}

// Recorder 记录链路收发消息。字节切片按引用保存（不拷贝），
// 用于缓冲区别名检测（报告 #5）；需要隔离时由调用方自行拷贝。
type Recorder struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (r *Recorder) Receive(b []byte) error {
	r.mu.Lock()
	r.msgs = append(r.msgs, b)
	r.mu.Unlock()
	return nil
}

// ReceiveError 返回一个记录消息后固定返回 err 的接收函数，
// 用于验证下游错误向上传播并中断处理。
func (r *Recorder) ReceiveError(err error) func([]byte) error {
	return func(b []byte) error {
		_ = r.Receive(b)
		return err
	}
}

func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func (r *Recorder) Get(i int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.msgs[i]
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	r.msgs = nil
	r.mu.Unlock()
}

// Pump 持续读取 r 并喂入链首（模拟 receiverRun）。
// 每次读取前拷贝字节，隔离报告 #5 的缓冲区别名对加密类测试的干扰。
// 链路返回错误时关闭 r 并把错误发送到返回的 channel。
func Pump(r io.ReadCloser, first netMiddleware.Middleware) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				if err := first.ReceiveData(data); err != nil {
					_ = r.Close()
					errCh <- err
					return
				}
			}
			if readErr != nil {
				_ = r.Close()
				return
			}
		}
	}()
	return errCh
}

// SeqPayload 生成带序号的完整性校验载荷：[4B seq] + 按 seq 派生的填充模式。
func SeqPayload(seq uint32, size int) []byte {
	b := make([]byte, size)
	binary.BigEndian.PutUint32(b, seq)
	for i := 4; i < size; i++ {
		b[i] = byte(seq) ^ byte(i)
	}
	return b
}

// VerifySeqPayload 校验 SeqPayload 的模式一致性，返回其序号。
func VerifySeqPayload(b []byte) (uint32, error) {
	if len(b) < 4 {
		return 0, fmt.Errorf("payload too short: %d", len(b))
	}
	seq := binary.BigEndian.Uint32(b)
	for i := 4; i < len(b); i++ {
		if b[i] != byte(seq)^byte(i) {
			return seq, fmt.Errorf("payload corrupt at index %d (seq %d)", i, seq)
		}
	}
	return seq, nil
}
