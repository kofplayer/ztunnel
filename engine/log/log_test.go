package log

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAllLogs(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir("./log")
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join("./log", e.Name()))
		if err != nil {
			t.Fatalf("read log file: %v", err)
		}
		sb.Write(b)
	}
	return sb.String()
}

// 固化日志级别门控语义：方法级别 L 的输出条件是 logLevel <= L。
// 历史缺陷：Warn 曾误用 INFO 常量判断（log_level≥2 时 WARN 被吞），
// 已在 commit 3073226 修复——本用例锁死修复后的事实，防止回退。
func TestLog_LevelGating(t *testing.T) {
	markers := []string{"[DEBUG]", "[INFO]", "[WARN]", "[ERROR]", "[FATAL]"}
	for level := int32(0); level <= int32(NONE); level++ {
		t.Run("level"+fmt.Sprint(level), func(t *testing.T) {
			t.Chdir(t.TempDir())
			l := NewLog()
			if err := l.Init("./log", "t_"); err != nil {
				t.Fatalf("init log: %v", err)
			}
			l.SetLogLevel(level)
			l.Debug("m-debug")
			l.Info("m-info")
			l.Warn("m-warn")
			l.Error("m-error")
			l.Fatal("m-fatal")
			content := readAllLogs(t)
			for lv, marker := range markers {
				want := int32(lv) >= level
				if got := strings.Contains(content, marker); got != want {
					t.Fatalf("logLevel=%d 时 %s 输出=%v，期望=%v", level, marker, got, want)
				}
			}
			_ = l.Uninit()
		})
	}
}
