package log

import (
	"os"
	"path/filepath"
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
// Logger.isNeedChangeFile —— 逐级（年/月/日/时）比较的各个分支
// ---------------------------------------------------------------------------

func TestLogger_IsNeedChangeFile(t *testing.T) {
	base := time.Date(2026, 3, 5, 10, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		in   time.Time
		want bool
	}{
		{"相等时刻不轮转", base, false},
		{"过去时刻不轮转", base.Add(-3 * time.Hour), false},
		{"跨年", base.AddDate(1, 0, 0), true},
		{"跨月", base.AddDate(0, 1, 0), true},
		{"跨日", base.AddDate(0, 0, 1), true},
		{"跨小时", base.Add(time.Hour), true},
		{"同小时内更晚的分钟", base.Add(59 * time.Minute), false},
		{"年内更早的月不算轮转", base.AddDate(0, -1, 0), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &Logger{createFileTime: base}
			eq(t, tc.want, l.isNeedChangeFile(tc.in), "是否需要轮转")
		})
	}
}

// ---------------------------------------------------------------------------
// Logger.DoLog：写入、轮转与空 logger 分支
// ---------------------------------------------------------------------------

func TestLogger_DoLog_WritesIntoHourFile(t *testing.T) {
	l, dir := newTestLogger(t, "rot_")

	noErr(t, l.DoLog("hello rotation", time.Now()), "DoLog")

	names := filesIn(t, dir)
	eq(t, 1, len(names), "应恰好生成一个日志文件")
	eq(t, "rot_"+time.Now().Format("2006010215")+".log", names[0], "文件名格式")

	body, err := os.ReadFile(filepath.Join(dir, names[0]))
	noErr(t, err, "ReadFile")
	truth(t, len(body) > 0, "日志内容不应为空")
	noErr(t, l.EndLog(), "EndLog")
}

// 跨小时后必须触发轮转。
//
// 注：新文件名由 openLogger 用 time.Now() 计算，而 StartLog 也是同一小时内建的，
// 因此同一小时内轮转只会重新打开**同名**文件（文件数仍为 1）；要看到第二个文件
// 必须真的跨过整点。所以这里断言的是轮转机制本身，而不是文件个数。
func TestLogger_DoLog_RotatesOnHourRollover(t *testing.T) {
	l, dir := newTestLogger(t, "hr_")
	eq(t, 1, len(filesIn(t, dir)), "起始应有一个文件")

	original := l.createFileTime
	truth(t, !original.IsZero(), "StartLog 应记录建文件时刻")

	// 伪造"文件是上一小时创建的"
	l.createFileTime = original.Add(-90 * time.Minute)

	noErr(t, l.DoLog("after rollover", time.Now()), "轮转后的 DoLog")

	truth(t, !l.createFileTime.Equal(original.Add(-90*time.Minute)),
		"轮转后 createFileTime 应被 openLogger 刷新")
	truth(t, time.Since(l.createFileTime) < time.Minute,
		"轮转后记录的时刻应接近当前，实际 "+l.createFileTime.String())
	truth(t, l.logger != nil, "轮转后应持有可用的 logger")

	// 轮转后继续写入必须仍然落盘
	noErr(t, l.DoLog("second line", time.Now()), "轮转后继续写入")
	noErr(t, l.EndLog(), "EndLog")

	body, err := os.ReadFile(filepath.Join(dir, filesIn(t, dir)[0]))
	noErr(t, err, "ReadFile")
	truth(t, contains(string(body), "after rollover") && contains(string(body), "second line"),
		"轮转前后的内容都应在文件中: "+string(body))
}

// 轮转时新开文件失败必须把错误回传（而不是继续往已关闭的后端写）。
func TestLogger_DoLog_RotationFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	l := new(Logger)
	noErr(t, l.StartLog(dir, "fail_", ""), "StartLog")

	// 让重开必然失败：把 logPath 换成一个"名字已被普通文件占用"的目录
	blocker := filepath.Join(dir, "blocker")
	noErr(t, os.WriteFile(blocker, []byte("x"), 0o600), "WriteFile")
	l.logPath = filepath.Join(blocker, "sub")
	l.createFileTime = time.Now().Add(-90 * time.Minute)

	wantErr(t, l.DoLog("must fail", time.Now()), "轮转失败时应返回 error")
	_ = l.EndLog()
}

// logger 为 nil 且无需轮转时必须返回错误，而不是解引用 nil ILogger。
func TestLogger_DoLog_NilLoggerReturnsError(t *testing.T) {
	now := time.Now()
	l := &Logger{logPath: t.TempDir(), createFileTime: now, logger: nil}
	wantErr(t, l.DoLog("boom", now), "logger 为 nil 时应返回错误而非 panic")
}

// EndLog 必须幂等：closeLogger 在 logger 已为 nil 时直接返回。
func TestLogger_EndLogIsIdempotent(t *testing.T) {
	l, _ := newTestLogger(t, "end_")
	noErr(t, l.EndLog(), "首次 EndLog")
	noErr(t, l.EndLog(), "重复 EndLog 不应 panic")
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

// 表征测试：EndLog 把 logger 置 nil 后再写会 nil panic（报告 CRASH-02 同族）。
// 本用例钉住现状；若改为返回 error，请更新断言。
func TestFileLogger_DoLogAfterEndPanics(t *testing.T) {
	dir := t.TempDir()
	f := new(FileLogger)
	noErr(t, f.StartLog(filepath.Join(dir, "a.log")), "StartLog")
	noErr(t, f.DoLog("line one"), "DoLog")
	noErr(t, f.EndLog(), "EndLog")

	panicked := func() (v bool) {
		defer func() {
			if recover() != nil {
				v = true
			}
		}()
		_ = f.DoLog("after end")
		return false
	}()
	truth(t, panicked, "当前实现 EndLog 之后 DoLog 会 nil panic；若已修复请更新本用例")
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
	li.SetLogLevel(NONE) // 全部屏蔽

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

// Init 成功后必须能真正写入并落盘。
func TestLogImp_InitThenWrite(t *testing.T) {
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
