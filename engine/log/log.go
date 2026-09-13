package log

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"sync"
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
	// mu 保护下面所有字段。
	//
	// 修复 H-1：此前 Logger 整条链**一个锁都没有**。到点轮转时，goroutine A 在
	// closeLogger 里把 logger 置 nil 并关闭文件，goroutine B 正在 FileLogger.DoLog
	// 里读同一个字段 → 数据竞争；更糟的是 A、B 都判定"需要轮转"、各自 openLogger
	// 开出两个文件，后写者覆盖 this.logger → **前一个 *os.File 及其 fd 永久泄漏**，
	// 日志被拆成两份。DoLog 现在全程持锁。
	mu             sync.Mutex
	logPath        string
	fileHead       string
	fileTail       string
	createFileTime time.Time
	// fileName 是当前正在写入的文件名。轮转判定改为"比较该时刻对应的文件名"，
	// 不再逐级比较年/月/日/时（见 isNeedChangeFile）。
	fileName string
	logger   ILogger
}

func (this *Logger) fileNameFor(t time.Time) string {
	return fmt.Sprintf("%s%04d%02d%02d%02d%s.log",
		this.fileHead, t.Year(), int(t.Month()), t.Day(), t.Hour(), this.fileTail)
}

// openLogger 调用方必须持锁。now 由调用方传入，避免同一次操作里两次取时钟。
func (this *Logger) openLogger(now time.Time) error {
	err := os.MkdirAll(this.logPath, os.ModePerm)
	if err != nil {
		return err
	}
	fileName := this.fileNameFor(now)
	lg := new(FileLogger)
	filePath := path.Join(this.logPath, fileName)
	if err := lg.StartLog(filePath); err != nil {
		return err
	}
	this.logger = lg
	this.fileName = fileName
	this.createFileTime = now
	return nil
}

// closeLogger 调用方必须持锁。关闭错误不再被吞掉。
func (this *Logger) closeLogger() error {
	if this.logger != nil {
		err := this.logger.EndLog()
		this.logger = nil
		this.fileName = ""
		return err
	}
	return nil
}

// isNeedChangeFile 判断 t 时刻是否应该换文件。
//
// 修复 L-11：原实现逐级比较"旧的 < 新的"（年→月→日→时），因此**只会朝前轮转**。
// 时钟回拨或夏令时回退时全部判为 false，日志会一直写进"未来那个小时"的文件里，
// 文件名与实际时刻不符、且再也无法纠正。按文件名比较则对回拨同样成立：
// 名字不同就换文件（回拨时是重新打开更早那个小时的的文件继续追加）。
func (this *Logger) isNeedChangeFile(t time.Time) bool {
	return this.fileName != this.fileNameFor(t)
}

// StartLog 初始化日志文件。
func (this *Logger) StartLog(logPath string, fileHead string, fileTail string) error {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.logPath = logPath
	this.fileHead = fileHead
	this.fileTail = fileTail
	return this.openLogger(time.Now())
}

func (this *Logger) EndLog() error {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.closeLogger()
}

// DoLog 写入一条日志，必要时先轮转。全程持锁（见 Logger.mu 的说明）。
func (this *Logger) DoLog(msg string, t time.Time) error {
	this.mu.Lock()
	defer this.mu.Unlock()

	// 先判 nil 且不自动重开：EndLog/Uninit 之后若悄悄把文件再打开，等于让一个
	// 已经声明关闭的 logger 复活继续写盘——比丢一条日志更难排查。
	if this.logger == nil {
		return fmt.Errorf("logger is nil")
	}

	if this.isNeedChangeFile(t) {
		if err := this.closeLogger(); err != nil {
			return fmt.Errorf("close log file: %w", err)
		}
		if err := this.openLogger(t); err != nil {
			return fmt.Errorf("createLogFile %v", err)
		}
	}

	// 不再吞掉底层写入错误：原先 closeLogger/openLogger/Output 的错误全部丢弃，
	// 磁盘满或文件被关掉时日志静默消失（报告 H-1）。
	return this.logger.DoLog(msg)
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

// 说明：Logger 的 openLogger/closeLogger/isNeedChangeFile/StartLog/EndLog/DoLog
// 已整体上移到 Logger 结构体定义处（加锁版本），此处不再重复定义。

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
		// 走加锁的 EndLog，而不是直接调 closeLogger（后者约定调用方必须持锁）
		_ = this.logerAll.EndLog()
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
