package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

const crashProbeEnv = "ZTUNNEL_CRASH_PROBE"

// InCrashProbe 返回当前是否处于崩溃探针子进程中。
//
// 用法（回归"未修复前会 panic"的缺陷，如报告 #1/#2/#14）：
//
//	func TestX_NoPanic(t *testing.T) {
//		if testutil.InCrashProbe() {
//			doDanger() // 未修复时在此 panic，子进程非零退出
//			return
//		}
//		if err := testutil.RunCrashProbe(t); err != nil {
//			t.Fatalf("回归 #N 未修复: %v", err)
//		}
//	}
//
// 采用子进程而非直接调用：修复前表现为"受控的失败"（父测试红、二进制不崩），
// 修复后子进程干净退出、用例转绿。
func InCrashProbe() bool {
	return os.Getenv(crashProbeEnv) == "1"
}

// RunCrashProbe 在子进程中单独运行当前测试，返回非 nil 表示子进程异常退出。
func RunCrashProbe(t *testing.T) error {
	t.Helper()
	cmd := exec.Command(os.Args[0],
		"-test.run", "^"+regexp.QuoteMeta(t.Name())+"$",
		"-test.count=1",
		"-test.timeout", "120s")
	cmd.Env = append(os.Environ(), crashProbeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := string(out)
	if len(msg) > 800 {
		msg = msg[len(msg)-800:]
	}
	return fmt.Errorf("crash probe exit: %v\n--- child output (tail) ---\n%s", err, msg)
}
