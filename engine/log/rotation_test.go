package log

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 本文件是包内测试（需访问 Logger/logImp 等非导出符号），因此**不能**引用
// testutil —— testutil 依赖本包，会造成 import cycle。断言用下面的本地小工具。

func noErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", what, err)
	}
}

func wantErr(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", what)
	}
}

func truth(t *testing.T, cond bool, what string) {
	t.Helper()
	if !cond {
		t.Fatalf("assertion failed: %s", what)
	}
}

func eq[T comparable](t *testing.T, want, got T, what string) {
	t.Helper()
	if want != got {
		t.Fatalf("%s: want %v, got %v", what, want, got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// newTestLogger 在临时目录里起一个 Logger，返回它与该目录。
func newTestLogger(t *testing.T, head string) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	l := new(Logger)
	noErr(t, l.StartLog(dir, head, ""), "StartLog")
	return l, dir
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	noErr(t, err, "ReadDir")
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// isNeedChangeFile —— 按文件名比较（L-11）
// ---------------------------------------------------------------------------

// 回归 L-11：轮转判定原先逐级比较"旧 < 新"（年→月→日→时），因此**只会朝前轮转**。
// 时钟回拨或夏令时回退时四个条件全为 false，日志会一直写进"未来那个小时"的文件，
// 文件名与实际时刻不符且再也无法纠正。改为比较文件名后，朝任何方向的时段变化都轮转。
func TestLogger_IsNeedChangeFile(t *testing.T) {
	// 基准取 10:30 而不是整点：这样 ±20 分钟仍落在同一个小时内，才能真正测到
	// "同小时内不轮转"。（若用 10:00，-30 分钟得到 09:30，已是另一个小时。）
	base := time.Date(2026, 3, 5, 10, 30, 0, 0, time.Local)

	cases := []struct {
		name    string
		have    time.Time // 当前文件对应的时刻
		in      time.Time // 待判定时刻
		want    bool
		comment string
	}{
		{"同一小时内更早的分钟不轮转", base, base.Add(-20 * time.Minute), false, ""},
		{"同一小时内更晚的分钟不轮转", base, base.Add(20 * time.Minute), false, ""},
		{"完全相同不轮转", base, base, false, ""},
		{"向前跨一小时", base, base.Add(time.Hour), true, ""},
		{"向后跨一小时（时钟回拨）", base, base.Add(-time.Hour), true,
			"L-11 未修复：回拨时旧实现判为不需要轮转，日志会一直写进错误的小时文件"},
		{"跨年", base, base.AddDate(1, 0, 0), true, ""},
		{"回拨到跨年", base, base.AddDate(-1, 0, 0), true, "L-11 未修复：回拨方向不轮转"},
		{"换月", base, base.AddDate(0, 1, 0), true, ""},
		{"换日", base, base.AddDate(0, 0, 1), true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &Logger{fileHead: "k_", fileName: fmt.Sprintf("k_%04d%02d%02d%02d.log",
				tc.have.Year(), int(tc.have.Month()), tc.have.Day(), tc.have.Hour())}
			got := l.isNeedChangeFile(tc.in)
			if got != tc.want {
				msg := fmt.Sprintf("isNeedChangeFile(%v) = %v, 期望 %v", tc.in, got, tc.want)
				if tc.comment != "" {
					msg += " — " + tc.comment
				}
				t.Fatal(msg)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DoLog：写入、轮转、错误传播
// ---------------------------------------------------------------------------

func TestLogger_DoLog_WritesIntoHourFile(t *testing.T) {
	l, dir := newTestLogger(t, "rot_")
	// 用 openLogger 实际记录的时刻作基准，而不是再取一次 time.Now()：
	// 后者若恰好跨过整点会意外触发轮转，让"只有一个文件"的断言随机失败。
	base := l.createFileTime

	noErr(t, l.DoLog("hello rotation", base), "DoLog")

	names := filesIn(t, dir)
	eq(t, 1, len(names), "应恰好生成一个日志文件")
	eq(t, "rot_"+base.Format("2006010215")+".log", names[0], "文件名格式")

	body, err := os.ReadFile(filepath.Join(dir, names[0]))
	noErr(t, err, "ReadFile")
	truth(t, len(body) > 0, "日志内容不应为空")
	noErr(t, l.EndLog(), "EndLog")
}

// 换到另一个小时必须新开文件，且旧文件被正常关闭、内容各归各位。
func TestLogger_DoLog_RotatesOnHourRollover(t *testing.T) {
	l, dir := newTestLogger(t, "hr_")
	now := l.createFileTime // 基准取实际建文件时的时刻
	eq(t, 1, len(filesIn(t, dir)), "起始应有一个文件")

	// 显式传入"下一个小时"的时刻，让轮转判定确定性成立（不依赖真实时钟跨过整点）
	next := now.Add(time.Hour)
	noErr(t, l.DoLog("after rollover", next), "轮转后的 DoLog")

	names := filesIn(t, dir)
	eq(t, 2, len(names), "跨小时后应新开一个文件")
	truth(t, names[0] != names[1], "两个轮转文件名不应相同")

	// 两条内容必须分别落在各自小时的文件中
	oldBody, err := os.ReadFile(filepath.Join(dir, "hr_"+now.Format("2006010215")+".log"))
	noErr(t, err, "读旧文件")
	newBody, err := os.ReadFile(filepath.Join(dir, "hr_"+next.Format("2006010215")+".log"))
	noErr(t, err, "读新文件")
	truth(t, contains(string(newBody), "after rollover"), "新时刻的日志应写进新文件")
	truth(t, !contains(string(oldBody), "after rollover"),
		"内容不应同时出现在旧文件里（说明轮转没真正切换文件句柄）")

	noErr(t, l.EndLog(), "EndLog")
}

// 时钟回拨时同样要轮转回正确的小时文件（L-11 的实际后果）。
func TestLogger_DoLog_RotatesOnClockRollback(t *testing.T) {
	l, dir := newTestLogger(t, "rb_")
	now := l.createFileTime // 基准取实际建文件时的时刻

	prev := now.Add(-time.Hour)
	noErr(t, l.DoLog("after rollback", prev), "回拨时刻的 DoLog")

	names := filesIn(t, dir)
	eq(t, 2, len(names), "L-11 未修复：时钟回拨时不轮转，日志写进了错误的小时文件")

	body, err := os.ReadFile(filepath.Join(dir, "rb_"+prev.Format("2006010215")+".log"))
	noErr(t, err, "读回拨时刻对应的文件")
	truth(t, contains(string(body), "after rollback"), "回拨后的日志应落在其真实小时的文件里")
	noErr(t, l.EndLog(), "EndLog")
}

// logger 为 nil 时返回错误，且**不会悄悄把文件重新打开**。
func TestLogger_DoLog_NilLoggerReturnsError(t *testing.T) {
	dir := t.TempDir()
	l := &Logger{logPath: dir, fileHead: "nil_", fileName: "nil_x.log"}

	wantErr(t, l.DoLog("boom", time.Now()), "logger 为 nil 时应返回错误而非 panic")
	eq(t, 0, len(filesIn(t, dir)), "EndLog/Uninit 之后不得因一次写入就把文件复活")
}

// 回归 L-10：底层写入错误必须原样上抛，不能再吞掉。
// （旧实现是 `this.logger.Output(...)` 后恒 `return nil`，磁盘满时日志静默消失。）
func TestLogger_DoLog_PropagatesBackendError(t *testing.T) {
	sentinel := errors.New("no space left on device")
	now := time.Now()
	l := &Logger{
		logPath:  t.TempDir(),
		fileName: "", // 下面用 fileNameFor 生成，保证不触发轮转
		logger:   &errLogger{err: sentinel},
	}
	l.fileName = l.fileNameFor(now)

	err := l.DoLog("must surface", now)
	if !errors.Is(err, sentinel) {
		t.Fatalf("L-10 未修复：底层写入错误被吞掉, got %v", err)
	}
}

type errLogger struct{ err error }

func (e *errLogger) StartLog(interface{}) error         { return nil }
func (e *errLogger) EndLog() error                      { return nil }
func (e *errLogger) DoLog(string, ...interface{}) error { return e.err }

// 轮转时新开文件失败必须把错误回传（而不是继续往已关闭的后端写）。
func TestLogger_DoLog_RotationFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	l := new(Logger)
	noErr(t, l.StartLog(dir, "fail_", ""), "StartLog")

	// 让重开必然失败：把 logPath 指向"名字已被普通文件占用"的路径
	blocker := filepath.Join(dir, "blocker")
	noErr(t, os.WriteFile(blocker, []byte("x"), 0o600), "WriteFile")
	l.logPath = filepath.Join(blocker, "sub")
	l.fileName = "fail_1970010100.log" // 强制触发轮转

	wantErr(t, l.DoLog("must fail", time.Now()), "轮转失败时应返回 error")
	_ = l.EndLog()
}

// EndLog 必须幂等：closeLogger 在 logger 已为 nil 时直接返回。
func TestLogger_EndLogIsIdempotent(t *testing.T) {
	l, _ := newTestLogger(t, "end_")
	noErr(t, l.EndLog(), "首次 EndLog")
	noErr(t, l.EndLog(), "重复 EndLog 不应 panic")
}

// ---------------------------------------------------------------------------
// H-1：并发轮转
// ---------------------------------------------------------------------------

// 回归 H-1：Logger 整条链原先**一个锁都没有**。
//
// 到点轮转时，goroutine A 在 closeLogger 里把 logger 置 nil 并关闭文件，
// goroutine B 正在 FileLogger.DoLog 里读同一个字段 → 数据竞争（本用例配 -race
// 即为红→绿证据）；更糟的是 A、B 同时判定需要轮转、各自 openLogger 开出两个
// 文件，后写者覆盖 this.logger → **前一个 *os.File 及其 fd 永久泄漏**。
func TestLogger_ConcurrentDoLog_IsRaceFree(t *testing.T) {
	l, dir := newTestLogger(t, "cc_")
	// 基准取 openLogger 记录的时刻：再调 time.Now() 有可能已经跨了整点，
	// 那样"只产生 2 个文件"的断言会随机失败。
	now := l.createFileTime

	const writers, perWriter = 12, 200
	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// 交替使用两个小时的时刻，制造密集的轮转路径
				ts := now
				if i%2 == 0 {
					ts = now.Add(time.Hour)
				}
				if err := l.DoLog(fmt.Sprintf("w%d-%d", w, i), ts); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("并发写入出现错误: %v", err)
	}
	noErr(t, l.EndLog(), "EndLog")

	// 只应产生这两个小时对应的文件：并发轮转不得开出多余文件
	names := filesIn(t, dir)
	eq(t, 2, len(names), "并发轮转产生了多余的文件（说明 openLogger 被重复执行）")

	total := 0
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		noErr(t, err, "ReadFile")
		total += len(b)
	}
	truth(t, total > 0, "并发写入的内容应完整落盘")
}

// ---------------------------------------------------------------------------
// FileLogger
// ---------------------------------------------------------------------------

func TestFileLogger_StartLog_RejectsNonStringParam(t *testing.T) {
	f := new(FileLogger)
	wantErr(t, f.StartLog(12345), "StartLog 只接受字符串路径")
	wantErr(t, f.StartLog(nil), "nil 参数也应被拒绝")
}

func TestFileLogger_StartLog_OnUnreachablePath(t *testing.T) {
	f := new(FileLogger)
	bad := filepath.Join(t.TempDir(), "no-such-dir", "x.log")
	wantErr(t, f.StartLog(bad), "目录不存在时应返回错误")
}

// 回归 L-10 / CRASH-02 同族：EndLog 之后再写必须返回错误而不是 nil panic。
func TestFileLogger_DoLogAfterEndReturnsError(t *testing.T) {
	dir := t.TempDir()
	f := new(FileLogger)
	noErr(t, f.StartLog(filepath.Join(dir, "a.log")), "StartLog")
	noErr(t, f.DoLog("line one"), "DoLog")
	noErr(t, f.EndLog(), "EndLog")

	wantErr(t, f.DoLog("after end"), "EndLog 之后写入应返回错误而非 panic")
	noErr(t, f.EndLog(), "重复 EndLog 应幂等")

	// 未启动就直接写同样必须报错
	f2 := new(FileLogger)
	wantErr(t, f2.DoLog("never started"), "未 StartLog 就写入应返回错误")
	noErr(t, f2.EndLog(), "未 StartLog 就 EndLog 应安全")
}

// EndLog 必须把关闭错误回传（原先恒返回 nil）。
func TestFileLogger_EndLog_ReturnsCloseError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.log")
	f := new(FileLogger)
	noErr(t, f.StartLog(p), "StartLog")

	// 外部先关掉同一个句柄是安全的；这里只验证正常关闭返回 nil
	noErr(t, f.EndLog(), "EndLog 应返回 nil")

	// 重新打开后删除文件再关闭：验证错误不被吞（某些平台上仍返回 nil，故不强求）
	noErr(t, f.StartLog(p), "重新 StartLog")
	_ = f.EndLog()
}

// ---------------------------------------------------------------------------
// logImp
// ---------------------------------------------------------------------------

func TestLogImp_DoLog_RejectsInvalidLevel(t *testing.T) {
	li := new(logImp)
	li.SetLogLevel(0)

	wantErr(t, li.doLog(99, "x"), "越界级别必须报错")
	wantErr(t, li.doLog(-1, "x"), "负级别必须报错")
	wantErr(t, li.doLog(len(levelNames), "x"), "等于级数的级别越界")
}

// 回归 CRASH-02 的可达形态：Init 失败后 logerAll 为 nil，此时任何日志调用
// 都不得 panic——连接收发循环的 recoverPanic 兜底路径也要写日志，兜底的兜底
// 再 panic 就等于直接杀死进程。
func TestLogImp_DoLog_BeforeInit_DoesNotPanic(t *testing.T) {
	li := new(logImp) // 从未 Init
	wantErr(t, li.doLog(INFO, "should not panic"), "未初始化时 doLog 应返回错误")

	entries := []struct {
		name string
		call func(string, ...interface{}) error
	}{
		{"Debug", li.Debug}, {"Info", li.Info}, {"Warn", li.Warn},
		{"Error", li.Error}, {"Fatal", li.Fatal},
	}
	for _, e := range entries {
		li.SetLogLevel(0)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s 在未初始化时 panic: %v", e.name, r)
				}
			}()
			wantErr(t, e.call("level entry"), "未初始化时应返回 error")
		}()
	}
}

// 级别过滤：高于当前阈值的调用应直接返回 nil，不触碰任何后端。
func TestLogImp_LevelGatingSkipsBackend(t *testing.T) {
	li := new(logImp)
	li.SetLogLevel(NONE)

	// logerAll 为 nil：若过滤失效就会走到后端并返回 "logger not initialized"
	noErr(t, li.Debug("dropped"), "DEBUG 应被 NONE 级别过滤")
	noErr(t, li.Info("dropped"), "INFO 应被 NONE 级别过滤")
	noErr(t, li.Warn("dropped"), "WARN 应被 NONE 级别过滤")
	noErr(t, li.Error("dropped"), "ERROR 应被 NONE 级别过滤")
	noErr(t, li.Fatal("dropped"), "FATAL 应被 NONE 级别过滤")
}

// Uninit 在未初始化时也必须安全（closeLoggers 有判空）。
func TestLogImp_Uninit_BeforeInit(t *testing.T) {
	li := new(logImp)
	noErr(t, li.Uninit(), "未初始化时 Uninit 应安全")
}

func TestLogImp_Init_RejectsUnusablePath(t *testing.T) {
	li := new(logImp)
	blocker := filepath.Join(t.TempDir(), "file")
	noErr(t, os.WriteFile(blocker, []byte("x"), 0o600), "WriteFile")
	wantErr(t, li.Init(filepath.Join(blocker, "sub"), "p_"),
		"日志目录创建失败时应把错误回传（入口必须检查它）")
	wantErr(t, li.Info("after failed init"), "Init 失败后写日志应返回 error")
}

// Init 成功后必须能真正写入并落盘；Uninit 之后再写不得复活文件。
func TestLogImp_InitThenWriteThenUninit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	li := new(logImp)
	noErr(t, li.Init("./log", "ok_"), "Init")
	li.SetLogLevel(DEBUG)
	noErr(t, li.Info("payload %d", 42), "Info")

	names := filesIn(t, filepath.Join(dir, "log"))
	eq(t, 1, len(names), "应生成一个日志文件")
	body, err := os.ReadFile(filepath.Join(dir, "log", names[0]))
	noErr(t, err, "ReadFile")
	truth(t, contains(string(body), "payload 42"), "日志应包含写入内容: "+string(body))

	noErr(t, li.Uninit(), "Uninit")
	wantErr(t, li.Info("after uninit"), "Uninit 之后写入应报错而不是悄悄重开文件")
}

// ---------------------------------------------------------------------------
// 包级 Main / SetMainLog
// ---------------------------------------------------------------------------

func TestMainLog_RoundTripAndNilGuard(t *testing.T) {
	prev := Main()
	t.Cleanup(func() { SetMainLog(prev) })

	SetMainLog(nil)
	truth(t, Main() == nil, "SetMainLog(nil) 后 Main() 应为 nil")

	l, _ := newTestLogger(t, "main_")
	inner := &logImp{logerAll: l}
	inner.SetLogLevel(DEBUG)
	SetMainLog(inner)

	got := Main()
	truth(t, got != nil, "SetMainLog 之后 Main 应可用")
	noErr(t, got.Info("via main"), "通过 Main 写日志")
	_ = l.EndLog()
}
