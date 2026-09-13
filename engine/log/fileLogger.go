package log

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
)

type FileLogger struct {
	// mu 保护 file 与 logger 两个字段。
	//
	// Logger 层已经串行化了大部分访问，但 EndLog/DoLog 仍可能被不同路径调用
	// （例如轮转与外部直接 EndLog），而原先这里连字段可见性都没有保证：
	// EndLog 把 logger 置 nil 的同时另一个 goroutine 正在 DoLog 里读它。
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
	//msgChannel chan string
	closed bool
}

func (this *FileLogger) StartLog(param interface{}) error {
	filePath, ok := param.(string)
	if !ok {
		return errors.New("param is not string")
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, os.ModePerm)
	// 修复 L-10：perm 参数里原先混入了 os.ModeAppend。perm 只接受权限位，
	// 混入类型位虽不致命但语义错误、且依赖 umask 之外的隐式行为。
	if err != nil {
		return err
	}
	this.mu.Lock()
	this.file = f
	this.logger = log.New(f, "", 0)
	this.closed = false
	this.mu.Unlock()
	/*
		this.msgChannel = make(chan string, 1000)
		go func() {
			for {
				msg, ok := <-this.msgChannel
				if !ok {
					break
				}
				this.logger.Output(0, msg)
			}
			this.logger = nil
			this.file.Close()
		}()
	*/
	return nil
}

func (this *FileLogger) EndLog() error {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.logger = nil
	f := this.file
	this.file = nil
	this.closed = true
	if f == nil {
		return nil // 幂等：重复 EndLog / 未 StartLog 就 EndLog 都不报错
	}
	return f.Close()
}

// DoLog 写入一条并**返回**底层错误。
//
// 修复 L-10：原先丢弃 `Output` 的返回值并恒返回 nil —— 磁盘满、文件已被关闭时
// 日志静默消失，没有任何信号（报告 H-1 的一部分）。
// 同时 EndLog 之后不再 nil panic，而是返回明确错误。
func (this *FileLogger) DoLog(format string, args ...interface{}) error {
	//this.msgChannel <- fmt.Sprintf(format, args...)
	this.mu.Lock()
	defer this.mu.Unlock()

	if this.logger == nil {
		if this.closed {
			return errors.New("FileLogger: log file already closed")
		}
		return errors.New("FileLogger: not started")
	}
	return this.logger.Output(0, fmt.Sprintf(format, args...))
}
