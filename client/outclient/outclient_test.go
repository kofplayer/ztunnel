package outclient

import (
	"io"
	"net"
	"runtime"
	"testing"
	"time"

	"ztunnel/client/inclient"
	"ztunnel/common/proto"
	"ztunnel/testutil"
)

func newTestHandler() *handler {
	return &handler{
		outCli: &outClient{
			c:           make(chan bool, 2),
			inClientMgr: inclient.NewClientMgr(),
		},
	}
}

// ---- 回归用例（修复前红） ----

// 回归 #14：CreateTunnel 应答为空数据时 data[0] 越界 panic。
// （h.timer 预先赋值，隔离另一处 nil-timer panic，聚焦数据长度校验缺失。）
// 修复后：应返回错误，不得 panic。
func TestOutClient_CreateTunnel_EmptyData_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h := newTestHandler()
		h.timer = time.NewTimer(time.Hour)
		_ = h.OnMessage(0, proto.MsgIdCreateTunnel, []byte{})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #14 未修复：CreateTunnel 空数据触发 panic:\n%v", err)
	}
}

// 回归 #14：ConnectNew 数据不足 4 字节时 data[:SessionIDSize] 越界 panic。
func TestOutClient_ConnectNew_ShortData_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h := newTestHandler()
		_ = h.OnMessage(0, proto.MsgIdConnectNew, []byte{0, 1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #14 未修复：ConnectNew 短数据触发 panic:\n%v", err)
	}
}

// 回归 #14：ConnectData 数据不足 4 字节时越界 panic。
func TestOutClient_ConnectData_ShortData_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h := newTestHandler()
		_ = h.OnMessage(0, proto.MsgIdConnectData, []byte{0, 1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #14 未修复：ConnectData 短数据触发 panic:\n%v", err)
	}
}

// 回归 #14：ConnectDelete 数据不足 4 字节时越界 panic。
func TestOutClient_ConnectDelete_ShortData_NoPanic(t *testing.T) {
	if testutil.InCrashProbe() {
		testutil.SilentLog(t)
		h := newTestHandler()
		_ = h.OnMessage(0, proto.MsgIdConnectDelete, []byte{0, 1})
		return
	}
	if err := testutil.RunCrashProbe(t); err != nil {
		t.Fatalf("回归 #14 未修复：ConnectDelete 短数据触发 panic:\n%v", err)
	}
}

// ---- 基线行为固化 ----

// 基线：ConnectData 携带未知 connectId 时应静默忽略（无对应 inclient）。
func TestOutClient_ConnectData_UnknownConnectId_NoSend(t *testing.T) {
	testutil.SilentLog(t)
	h := newTestHandler()
	err := h.OnMessage(0, proto.MsgIdConnectData, []byte{0, 0, 0, 9, 1, 2, 3})
	testutil.NoError(t, err)
}

// 回归 #7：建隧道成功后（OnMessage 中 timer.Stop()），
// OnReady 启动的超时 goroutine 仍阻塞在 <-timer.C 永不退出 → 每次成功泄漏 1 个 goroutine。
// 修复前：goroutine 数稳定停在 基线+3（收/发/泄漏）；修复后：回落 基线+2。
func TestOutClient_TunnelEstablished_NoTimerGoroutineLeak(t *testing.T) {
	testutil.SilentLog(t)
	oldEnc := proto.NetEncrypt
	proto.NetEncrypt = false
	t.Cleanup(func() { proto.NetEncrypt = oldEnc })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	defer ln.Close()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)

	respWritten := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(respWritten)
			return
		}
		go func() { _, _ = io.Copy(io.Discard, c) }() // 排空客户端上行
		time.Sleep(100 * time.Millisecond)            // 保证 OnReady 已建立 timer goroutine
		// len4 帧：msgId=1(CreateTunnel) + 应答 0x00（成功）
		_, _ = c.Write([]byte{0, 0, 0, 2, 0x01, 0x00})
		close(respWritten)
	}()

	base := runtime.NumGoroutine()
	cli := NewClient("127.0.0.1", port, 9999, "127.0.0.1", 1)
	go func() { _ = cli.Start() }()

	<-respWritten
	time.Sleep(200 * time.Millisecond) // 客户端处理成功应答（OnMessage → timer.Stop()）

	// 此时存活 goroutine = 基线 + Start goroutine + 连接收发 + timer goroutine = 基线+4。
	// 修复前：timer goroutine 阻塞在 <-timer.C 永不退出，稳定为 基线+4；
	// 修复后：应答处理后退出，回落 基线+3。
	if !testutil.Eventually(t, 1500*time.Millisecond, func() bool {
		return runtime.NumGoroutine() <= base+3
	}) {
		t.Fatalf("回归 #7 未修复：建隧道成功后 timer goroutine 泄漏（当前 %d，基线 %d）",
			runtime.NumGoroutine(), base)
	}
	_ = cli.Stop()
}
