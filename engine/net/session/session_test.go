package netSession_test

import (
	"sync"
	"testing"
	"time"

	netSession "ztunnel/engine/net/session"
	"ztunnel/testutil"
)

func TestSession_CloseWithoutConn_Idempotent(t *testing.T) {
	sm := netSession.NewSessionMgr()
	s := sm.NewSession()
	testutil.NoError(t, s.Close())
	testutil.NoError(t, s.Close(), "Close 应幂等")
}

// 回归 CRASH-01：Close() 之后必须仍能取到 Conn 并调 RemoteAddr()。
//
// 此前 Close() 把 ns.conn 置 nil，而 OnDisconnect 回调链普遍用
// s.GetConn().RemoteAddr() 记诊断日志（inserver.go 有 11 处 GetConn）。
// 在 SEC-01 修好、断开通知真正开始派发之后，这条路必然 nil panic。
// net.Conn 关闭后调 RemoteAddr 本身是安全的（返回已缓存地址）。
func TestSession_CloseKeepsConnReadable(t *testing.T) {
	sm := netSession.NewSessionMgr()
	s := sm.NewSession()
	fc := &testutil.FakeConn{}
	s.SetConn(fc)

	testutil.NoError(t, s.Close())
	testutil.True(t, s.GetConn() != nil, "CRASH-01 未修复：Close() 把 conn 置为 nil")
	testutil.Equal(t, "203.0.113.7:44444", s.GetConn().RemoteAddr(), "关闭后仍应能取到远端地址")
	testutil.Equal(t, 1, fc.DisconnectCount(), "Close 应恰好触发一次 Disconnect")

	testutil.NoError(t, s.Close(), "Close 应幂等")
}

// 回归 CRASH-01 的并发面：会话的 conn/bindObject/sendMessageFunc 会被
// **其它连接的 goroutine** 读写（例如 inserver 在控制通道 receiver 上
// 对用户会话调 Close），必须全部持锁。
// 无 -race 时本用例靠"不 panic / 不死锁"检出，配 -race 才是决定性的。
func TestSession_ConcurrentFieldAccess(t *testing.T) {
	sm := netSession.NewSessionMgr()
	s := sm.NewSession()
	s.SetConn(&testutil.FakeConn{})
	s.SetSendMessageFunc(func(uint32, uint32, []byte) error { return nil })

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.SetBindObject(i)
				_ = s.GetBindObject()
				_ = s.GetConn()
				_ = s.SendMessage(0, 0, nil)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.Close()
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
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
