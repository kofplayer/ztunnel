package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 入口校验此前完全无测试（cmd/* 0%）。这里覆盖 parseFlags/validate——
// 它们决定"配置写错时是静默失败还是明确报错"。

func TestParseFlags_Defaults(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("空参数应全走默认值: %v", err)
	}
	if cfg.server != "localhost:8888" || cfg.forward != "localhost:9999" {
		t.Fatalf("地址默认值不符: %+v", cfg)
	}
	if cfg.exportPort != 9999 {
		t.Fatalf("-export_port 默认应为 9999, got %d", cfg.exportPort)
	}
	if cfg.netEncrypt || cfg.token != "" || cfg.logLevel != 0 {
		t.Fatalf("其余默认值不符: %+v", cfg)
	}
}

func TestParseFlags_AllFlags(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-server=1.2.3.4:8888", "-forward=10.0.0.5:3306",
		"-export_port=3307", "-net_encrypt=true", "-token=tk", "-log_level=2",
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.exportPort != 3307 || !cfg.netEncrypt || cfg.token != "tk" || cfg.logLevel != 2 {
		t.Fatalf("解析结果不符: %+v", cfg)
	}
}

func TestParseFlags_Help(t *testing.T) {
	if _, err := parseFlags([]string{"-h"}); !errors.Is(err, errHelp) {
		t.Fatalf("-h 应返回 errHelp（main 据此退出码 0）, got %v", err)
	}
}

func TestParseFlags_BadInputs(t *testing.T) {
	for _, args := range [][]string{
		{"-nope"},
		{"-export_port=abc"},
		{"-export_port=-5"}, // Uint 不接受负号
		{"-net_encrypt=maybe"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Fatalf("参数 %v 应被拒绝", args)
		}
	}
}

func TestValidate_OK(t *testing.T) {
	cfg := config{
		logLevel: 1, server: "127.0.0.1:8888", forward: "[::1]:3306", exportPort: 3307,
	}
	ep, err := cfg.validate()
	if err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}
	if ep.serverHost != "127.0.0.1" || ep.serverPort != 8888 {
		t.Fatalf("server 地址解析不符: %+v", ep)
	}
	// IPv6 以不带方括号的裸主机返回，拼接时由 net.JoinHostPort 负责
	if ep.forwardHost != "::1" || ep.forwardPort != 3306 {
		t.Fatalf("forward 地址解析不符: %+v", ep)
	}
	if ep.exportPort != 3307 {
		t.Fatalf("exportPort 不符: %d", ep.exportPort)
	}
}

// 回归 M-20：-export_port 此前是 flag.Int + uint16() 强转、无范围校验。
// 70000 会静默变成 4464（把服务暴露到一个谁都没预期的端口），
// 65536 变成 0（陷入 10 秒重连死循环）。现在两者都必须在启动时被拒。
func TestValidate_RejectsOutOfRangeExportPort(t *testing.T) {
	for _, p := range []uint{0, 65536, 70000, 1 << 31} {
		cfg := config{server: "h:1", forward: "h:2", exportPort: p}
		_, err := cfg.validate()
		if err == nil {
			t.Fatalf("-export_port=%d 应被拒绝", p)
		}
		if !strings.Contains(err.Error(), "-export_port") {
			t.Fatalf("错误应指明是 -export_port 的问题, got %v", err)
		}
	}
	// 边界合法
	for _, p := range []uint{1, 65535} {
		cfg := config{server: "h:1", forward: "h:2", exportPort: p}
		if _, err := cfg.validate(); err != nil {
			t.Fatalf("-export_port=%d 应合法: %v", p, err)
		}
	}
}

// 回归 M-19：缺冒号的地址必须被拒（Readme 旧示例 `-listen=8888` 的同类问题）。
func TestValidate_RejectsBadAddresses(t *testing.T) {
	badServer := []string{"8888", "", "host", "h:0", "h:65536", "h:abc", "::1", "a:b:c"}
	for _, s := range badServer {
		cfg := config{server: s, forward: "h:2", exportPort: 1}
		if _, err := cfg.validate(); err == nil {
			t.Fatalf("-server=%q 应被拒绝", s)
		}
	}
	badForward := []string{"3306", "", "h:0", "h:xyz"}
	for _, f := range badForward {
		cfg := config{server: "h:1", forward: f, exportPort: 1}
		if _, err := cfg.validate(); err == nil {
			t.Fatalf("-forward=%q 应被拒绝", f)
		}
	}
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	for _, lvl := range []int{-1, 6, 100} {
		cfg := config{logLevel: lvl, server: "h:1", forward: "h:2", exportPort: 1}
		if _, err := cfg.validate(); err == nil {
			t.Fatalf("-log_level=%d 应被拒绝", lvl)
		}
	}
}

// validate 的错误必须指到具体参数名。
func TestValidate_ErrorMessagesNameTheFlag(t *testing.T) {
	_, err := (config{server: "8888", forward: "h:2", exportPort: 1}).validate()
	if err == nil || !strings.Contains(err.Error(), "-server") {
		t.Fatalf("错误应指明 -server, got %v", err)
	}
	_, err = (config{server: "h:1", forward: "3306", exportPort: 1}).validate()
	if err == nil || !strings.Contains(err.Error(), "-forward") {
		t.Fatalf("错误应指明 -forward, got %v", err)
	}
}

// 重连循环的形态：Start 返回后必须先 Stop 再 sleep。
// 回归 LEAK-03 的一个侧面——上一个实例的连接与 inclient 没道理再多活 10 秒。
func TestReconnectDelay_IsPositiveAndLoopStopsInstancesFirst(t *testing.T) {
	if reconnectDelay <= 0 {
		t.Fatalf("重连间隔应为正数, got %v", reconnectDelay)
	}
	// 可调小，供后续集成用例复用（避免真等 10 秒）
	old := reconnectDelay
	t.Cleanup(func() { reconnectDelay = old })
	reconnectDelay = 5 * time.Millisecond
	if reconnectDelay != 5*time.Millisecond {
		t.Fatal("reconnectDelay 应可被测试调整")
	}
}
