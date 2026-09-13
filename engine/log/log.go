package log

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"sync/atomic"
	"time"
)

// _mainLog 用 atomic 存放：SetMainLog 写、Main 读可能发生在不同 goroutine。
// 这不只是理论问题——上一个用例遗留的接收 goroutine 在 OnDisconnect 里读
// log.Main()，与下一个用例的 SetMainLog 并发，已被 -race 实测报出 DATA RACE
// （报告 L-9）。
var _mainLog atomic.Pointer[Log]

func SetMainLog(l Log) {
	_mainLog.Store(&l)
}

func Main() Log {
	p := _mainLog.Load()
	if p == nil {
		return nil
	}
	return *p
}

func NewLog() Log {
	return new(logImp)
}

const (
	DEBUG = iota
	INFO
	WARN
	ERROR
	FATAL
	NONE
)

var levelNames = []string{
	"[DEBUG]",
	"[INFO]",
	"[WARN]",
	"[ERROR]",
	"[FATAL]",
}

type Logger struct {
	logPath        string
	fileHead       string
	fileTail       string
	createFileTime time.Time
	logger         ILogger
}

type Log interface {
	Init(logPath string, fileHead string) error
	Uninit() error
	SetLogLevel(level int32)
	Debug(format string, args ...interface{}) error
	Info(format string, args ...interface{}) error
	Warn(format string, args ...interface{}) error
	Error(format string, args ...interface{}) error
	Fatal(format string, args ...interface{}) error
}

type logImp struct {
	logPath  string
	logLevel int32
	fileHead string
	//loggers  []*Logger
	logerAll *Logger
}

func (this *Logger) openLogger() error {
	err := os.MkdirAll(this.logPath, os.ModePerm)
	if err != nil {
		return err
	}
	now := time.Now()
	fileName := fmt.Sprintf("%s%04d%02d%02d%02d%s.log", this.fileHead, now.Year(), now.Month(), now.Day(), now.Hour(), this.fileTail)
	this.logger = new(FileLogger)
	filePath := path.Join(this.logPath, fileName)
	err = this.logger.StartLog(filePath)
	if err != nil {
		return err
	}
	this.createFileTime = now
	return nil
}

func (this *Logger) closeLogger() error {
	if this.logger != nil {
		this.logger.EndLog()
		this.logger = nil
	}
	return nil
}

func (this *Logger) isNeedChangeFile(t time.Time) bool {
	if this.createFileTime.Year() < t.Year() {
		return true
	}
	if this.createFileTime.Month() < t.Month() {
		return true
	}
	if this.createFileTime.Day() < t.Day() {
		return true
	}
	if this.createFileTime.Hour() < t.Hour() {
		return true
	}
	return false
}

func (this *Logger) StartLog(logPath string, fileHead string, fileTail string) error {
	this.logPath = logPath
	this.fileHead = fileHead
	this.fileTail = fileTail
	err := this.openLogger()
	if err != nil {
		return err
	}
	return nil
}

func (this *Logger) EndLog() error {
	return this.closeLogger()
}

func (this *Logger) DoLog(msg string, t time.Time) error {

	if this.isNeedChangeFile(t) {
		this.closeLogger()
		if err := this.openLogger(); err != nil {
			return fmt.Errorf("createLogFile %v", err)
		}
	}

	if this.logger == nil {
		return fmt.Errorf("logger is nil")
	}

	return this.logger.DoLog(msg)
}

func (this *logImp) openLoggers() error {
	wdPath, _ := os.Getwd()
	logPath := path.Join(wdPath, this.logPath)
	err := os.MkdirAll(logPath, os.ModePerm)
	if err != nil {
		return err
	}
	// levelCount := len(levelNames)
	// this.loggers = make([]*Logger, levelCount, levelCount)
	// for k, v := range levelNames {
	// 	logger := new(Logger)
	// 	err = logger.StartLog(logPath, this.fileHead, v)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	this.loggers[k] = logger
	// }

	logger := new(Logger)
	err = logger.StartLog(logPath, this.fileHead, "")
	if err != nil {
		return err
	}
	this.logerAll = logger
	return nil
}

func (this *logImp) closeLoggers() error {
	// for _, v := range this.loggers {
	// 	v.closeLogger()
	// }
	if this.logerAll != nil {
		this.logerAll.closeLogger()
	}
	return nil
}

func (this *logImp) doLog(level int, format string, args ...interface{}) error {
	levelCount := len(levelNames)
	if level >= levelCount || level < 0 {
		return fmt.Errorf("invalid log level %v", level)
	}
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		file = "???"
		line = 0
	}
	now := time.Now()
	msg := fmt.Sprintf(format, args...)
	year, month, day := now.Date()
	hour, min, sec := now.Clock()

	fullMsg := fmt.Sprintf("%s%04d/%02d/%02d %02d:%02d:%02d %v:%v: %v", levelNames[level], year, month, day, hour, min, sec, file, line, msg)
	if level > DEBUG {
		fmt.Println(fullMsg)
	}
	// logerAll 只在 openLoggers 成功后才被赋值。Init 失败（例如 cwd 只读导致
	// MkdirAll/OpenFile 出错）时它是 nil，而 nil 接收者上调 DoLog 会在
	// `this.isNeedChangeFile` 里解引用 createFileTime → panic。连接收发循环的
	// recoverPanic 也要写日志，那等于"兜 panic 的兜底自身 panic"，会直接杀死
	// 进程、绕过 recover 语义（报告 CRASH-02）。因此这里降级到 stderr 并返回
	// error，**绝不 panic**。
	if this.logerAll == nil {
		fmt.Fprintln(os.Stderr, fullMsg)
		return errors.New("logger not initialized")
	}
	//this.loggers[level].DoLog(fullMsg, now)
	return this.logerAll.DoLog(fullMsg, now)
}

func (this *logImp) Init(logPath string, fileHead string) error {
	this.logPath = logPath
	this.fileHead = fileHead
	return this.openLoggers()
}

func (this *logImp) Uninit() error {
	return this.closeLoggers()
}

func (this *logImp) SetLogLevel(level int32) {
	atomic.StoreInt32(&this.logLevel, level)
}

func (this *logImp) Debug(format string, args ...interface{}) error {
	if this.logLevel > DEBUG {
		return nil
	}
	return this.doLog(DEBUG, format, args...)
}

func (this *logImp) Info(format string, args ...interface{}) error {
	if this.logLevel > INFO {
		return nil
	}
	return this.doLog(INFO, format, args...)
}

func (this *logImp) Warn(format string, args ...interface{}) error {
	if this.logLevel > WARN {
		return nil
	}
	return this.doLog(WARN, format, args...)
}

func (this *logImp) Error(format string, args ...interface{}) error {
	if this.logLevel > ERROR {
		return nil
	}
	return this.doLog(ERROR, format, args...)
}

func (this *logImp) Fatal(format string, args ...interface{}) error {
	if this.logLevel > FATAL {
		return nil
	}
	return this.doLog(FATAL, format, args...)
}
