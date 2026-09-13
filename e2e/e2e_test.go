// Package e2e 通过真实的 inserver + outclient 跑一条完整隧道。
//
// 既有测试全部是单元/handler 级：没有任何用例真正穿过隧道送过一次数据，
// 因此"隧道整体不工作"这类回归此前只能靠人工发现（报告 E-08）。
package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"ztunnel/client/outclient"
	"ztunnel/common/proto"
	netServer "ztunnel/engine/net/server"
	netSession "ztunnel/engine/net/session"
	"ztunnel/server/inserver"
	"ztunnel/testutil"
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	return uint16(testutil.FreePort(t))
}

func addrOf(port uint16) string { return "127.0.0.1:" + strconv.Itoa(int(port)) }

// canDial 探测某端口是否已在监听。
func canDial(port uint16) bool {
	c, err := net.DialTimeout("tcp", addrOf(port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// startEcho 起一个按行回显的真实服务，返回其端口。
func startEcho(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				for {
					line, err := r.ReadString('\n')
					if len(line) > 0 {
						if _, werr := w.WriteString("echo:" + line); werr != nil {
							return
						}
						if werr := w.Flush(); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// setupTunnel 起一条完整隧道并返回 export 端口。
//
// ⚠️ 必须**分阶段等待端口就绪**，不能 go svr.Start() 之后立刻 go cli.Start()：
// 客户端可能在服务端完成 bind 之前就拨号，Connect() 失败后 outclient.Start()
// 会直接返回（真实产品的重试在 cmd/client.go 的死循环里，测试没有那层）。
// 加 -race 后服务端 bind 变慢，这种写法会确定性建不起隧道。
func setupTunnel(t *testing.T, encrypted bool) (uint16, netServer.NetServer) {
	t.Helper()

	proto.SetToken("e2etoken")
	proto.NetEncrypt = encrypted
	t.Cleanup(func() {
		proto.SetToken("")
		proto.NetEncrypt = false
	})

	echoPort := startEcho(t)
	ctrlPort := freePort(t)
	exportPort := freePort(t)

	svr := inserver.NewServer("", ctrlPort, "")
	go func() { _ = svr.Start() }()
	t.Cleanup(func() { _ = svr.Stop() })

	if !testutil.Eventually(t, 10*time.Second, func() bool { return canDial(ctrlPort) }) {
		t.Fatalf("控制端口 %d 未在 10s 内进入监听", ctrlPort)
	}

	// 与 cmd/client.go 同构的重连循环：每轮**新建一个 client**，Start 返回后隔
	// 200ms 再来，直到 export 端口能被连上。
	//
	// 必须新建实例：netClient 连同其 connector 是一次性的（报告 M-12），复用同一
	// 个实例从第二轮起只会一直报 "not idle"，隧道再也起不来。
	var mu sync.Mutex
	current := outclient.NewClient("127.0.0.1", ctrlPort, exportPort, "127.0.0.1", echoPort)
	stop := make(chan struct{})
	loopDone := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		// 必须先 Stop 当前实例：循环可能正阻塞在 Start() 里，不断开连接它不返回。
		mu.Lock()
		c := current
		mu.Unlock()
		_ = c.Stop()
		<-loopDone
	})

	go func() {
		defer close(loopDone)
		for {
			mu.Lock()
			select {
			case <-stop:
				mu.Unlock()
				return
			default:
			}
			cli := outclient.NewClient("127.0.0.1", ctrlPort, exportPort, "127.0.0.1", echoPort)
			current = cli
			mu.Unlock()

			_ = cli.Start()

			select {
			case <-stop:
				_ = cli.Stop()
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()

	if !testutil.Eventually(t, 15*time.Second, func() bool { return canDial(exportPort) }) {
		t.Fatalf("隧道未在 15s 内建立：export 端口 %d 始终无法连接", exportPort)
	}
	return exportPort, svr
}

// 端到端：终端用户连公网 export 端口 → server 经控制通道转给内网 client →
// client 连真实服务 → 双向回显。两个方向、多轮往返都必须正确。
func TestE2E_TunnelRoundTrip(t *testing.T) {
	testutil.SilentLog(t)
	exportPort, svr := setupTunnel(t, false)

	conn, err := net.Dial("tcp", addrOf(exportPort))
	testutil.NoError(t, err)
	defer conn.Close()

	r := bufio.NewReader(conn)
	for i := 1; i <= 3; i++ {
		msg := "ping" + strconv.Itoa(i) + "\n"
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(conn, msg); err != nil {
			t.Fatalf("第 %d 轮写入失败: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("第 %d 轮读回显失败: %v", i, err)
		}
		testutil.Equal(t, "echo:"+msg, line, fmt.Sprintf("第 %d 轮回显内容不符", i))
	}
	conn.Close()

	// 用户断开后，outserver 侧的用户会话必须被回收（回归 SEC-01 的跨进程泄漏：
	// 此前 ConnectDelete 在主动关闭路径上根本不会发出）。
	// 注意查的是 outserver 自己的 SessionMgr——inserver 那个装的是控制会话，
	// 此刻 outclient 还连着，本就应该非 0。
	testutil.True(t, testutil.Eventually(t, 5*time.Second, func() bool {
		return boundUserSessions(svr) == 0
	}), "终端用户断开后 outserver 仍有残留用户会话")
}

// 端到端·并发：多条终端用户连接同时走同一条隧道。
// 覆盖控制通道被多个 goroutine 并发 SendMessage 的真实场景（type1 sendMu 的
// 存在理由），以及 connectId 多路复用的正确性。
func TestE2E_ConcurrentUsers(t *testing.T) {
	testutil.SilentLog(t)
	exportPort, _ := setupTunnel(t, false)

	const users, perUser = 8, 20
	results := make(chan error, users)
	for u := 0; u < users; u++ {
		go func(u int) {
			c, err := net.Dial("tcp", addrOf(exportPort))
			if err != nil {
				results <- err
				return
			}
			defer c.Close()
			r := bufio.NewReader(c)
			for i := 0; i < perUser; i++ {
				want := "echo:u" + strconv.Itoa(u) + "-" + strconv.Itoa(i) + "\n"
				if _, err := io.WriteString(c, "u"+strconv.Itoa(u)+"-"+strconv.Itoa(i)+"\n"); err != nil {
					results <- err
					return
				}
				c.SetReadDeadline(time.Now().Add(15 * time.Second))
				got, err := r.ReadString('\n')
				if err != nil {
					results <- err
					return
				}
				if got != want {
					results <- fmt.Errorf("回显串扰: want %q got %q", want, got)
					return
				}
			}
			results <- nil
		}(u)
	}
	for u := 0; u < users; u++ {
		select {
		case err := <-results:
			testutil.NoError(t, err, "并发用户出现问题")
		case <-time.After(90 * time.Second):
			t.Fatal("并发往返超时")
		}
	}
}

// 端到端·加密：-net_encrypt=true 时走完整 type1 握手 + verifier 后仍须正确转发。
func TestE2E_EncryptedControlChannel(t *testing.T) {
	testutil.SilentLog(t)
	exportPort, _ := setupTunnel(t, true)

	conn, err := net.Dial("tcp", addrOf(exportPort))
	testutil.NoError(t, err)
	defer conn.Close()

	r := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(conn, "secret\n"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	line, err := r.ReadString('\n')
	testutil.NoError(t, err, "加密隧道读回显失败")
	testutil.Equal(t, "echo:secret\n", line)
}

// --- 会话回收观测 ---

// boundUserSessions 经控制会话的 bindObject 取出 outserver，返回其用户会话数。
// 尚未建隧道时返回 -1。
func boundUserSessions(svr netServer.NetServer) int {
	found := -1
	svr.GetSessionMgr().TravelSession(func(s netSession.NetSession) bool {
		bo := s.GetBindObject()
		if bo == nil {
			return true
		}
		if out, ok := bo.(netServer.NetServer); ok {
			found = out.GetSessionMgr().Len()
			return false
		}
		return true
	})
	return found
}
