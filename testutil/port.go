package testutil

import (
	"net"
	"testing"
)

// FreePort 取一个当前空闲的 TCP 端口（存在 TOCTOU 窗口，失败自动重试）。
func FreePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 3; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		return port
	}
	t.Fatal("no free port available")
	return 0
}
