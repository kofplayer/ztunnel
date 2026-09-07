// Package testutil 提供零依赖的测试辅助设施，仅供测试代码引用。
//
// 设计约束：项目硬性要求零第三方依赖，因此断言/桩/工具全部基于标准库实现。
package testutil

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func failMsg(msgAndArgs ...any) string {
	if len(msgAndArgs) == 0 {
		return ""
	}
	return strings.TrimSuffix(fmt.Sprintln(msgAndArgs...), "\n")
}

func NoError(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v. %s", err, failMsg(msgAndArgs...))
	}
}

func Error(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil. %s", failMsg(msgAndArgs...))
	}
}

func Equal[T comparable](t *testing.T, want, got T, msgAndArgs ...any) {
	t.Helper()
	if want != got {
		t.Fatalf("not equal: want %v, got %v. %s", want, got, failMsg(msgAndArgs...))
	}
}

func BytesEqual(t *testing.T, want, got []byte, msgAndArgs ...any) {
	t.Helper()
	if !bytes.Equal(want, got) {
		t.Fatalf("bytes not equal:\nwant(len=%d): % x\ngot (len=%d): % x\n%s",
			len(want), want, len(got), got, failMsg(msgAndArgs...))
	}
}

func True(t *testing.T, cond bool, msgAndArgs ...any) {
	t.Helper()
	if !cond {
		t.Fatalf("condition failed. %s", failMsg(msgAndArgs...))
	}
}

// Eventually 在 timeout 内每 50ms 轮询一次 cond，满足返回 true。
// 全部异步断言应基于它而非 sleep，保证时序稳定。
func Eventually(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
