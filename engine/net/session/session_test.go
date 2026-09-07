package netSession_test

import (
	"testing"

	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

func TestSession_CloseWithoutConn_Idempotent(t *testing.T) {
	sm := netSession.NewSessionMgr()
	s := sm.NewSession()
	testutil.NoError(t, s.Close())
	testutil.NoError(t, s.Close(), "Close 应幂等")
}

// 回归：NewSession 在注册进 sessionMgr 之后、调用方 SetSendMessageFunc 之前存在窗口，
// 此窗口内其他 goroutine 拿到会话并 SendMessage 会触发 nil 函数调用 panic。
// 修复后：SendMessage 应返回错误而非 panic。
func TestSession_SendMessageBeforeFuncSet_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		sm := netSession.NewSessionMgr()
		_ = sm.NewSession().SendMessage(0, 0, []byte{1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归未修复：未设置 SendMessageFunc 时 SendMessage 触发 nil 调用 panic:\n%v", err)
	}
}
