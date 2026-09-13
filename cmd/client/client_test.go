package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	netClient "ztunnel/engine/net/client"
	socketNetConnect "ztunnel/engine/net/connect/socket"
)

// 入口校验此前完全无测试（cmd/* 0%）。这里覆盖 parseFlags/validate——
// 它们决定"配置写错时是静默失败还是明确报错"。

// baseConfig 返回一份除被测字段外全部合法的配置。
//
// 必须显式带上超时：validate() 里超时校验排在前面，省略它会让断言拿到
// 错误原因的失败信息，测试就失去归因意义。
func baseConfig() config {
	return config{
		logLevel: 0, server: "h:1", forward: "h:2", exportPort: 1,
		dialTimeout: 5 * time.Second, handshakeTo: 30 * time.Second,
	}
}

func tryValidate(cfg config) error {
	_, err := cfg.validate()
	return err
}

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
	// 默认值必须继承 engine 侧常量，而不是在入口再抄一份数字（否则两处会漂移）
	if cfg.dialTimeout != socketNetConnect.DialTimeout {
		t.Fatalf("-dial_timeout 默认应取 engine 值, got %v", cfg.dialTimeout)
	}
	if cfg.handshakeTo != netClient.HandshakeTimeout {
		t.Fatalf("-handshake_timeout 默认应取 engine 值, got %v", cfg.handshakeTo)
	}
}

func TestParseFlags_AllFlags(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-server=1.2.3.4:8888", "-forward=10.0.0.5:3306",
		"-export_port=3307", "-net_encrypt=true", "-token=tk", "-log_level=2",
		"-dial_timeout=2s", "-handshake_timeout=1m",
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.exportPort != 3307 || !cfg.netEncrypt || cfg.token != "tk" || cfg.logLevel != 2 {
		t.Fatalf("解析结果不符: %+v", cfg)
	}
	if cfg.dialTimeout != 2*time.Second || cfg.handshakeTo != time.Minute {
		t.Fatalf("超时解析结果不符: %+v", cfg)
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
		{"-dial_timeout=abc"},
		{"-handshake_timeout=abc"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Fatalf("参数 %v 应被拒绝", args)
		}
	}
}

func TestValidate_OK(t *testing.T) {
	cfg := baseConfig()
	cfg.logLevel = 1
	cfg.server = "127.0.0.1:8888"
	cfg.forward = "[::1]:3306"
	cfg.exportPort = 3307

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
		cfg := baseConfig()
		cfg.exportPort = p
		err := tryValidate(cfg)
		if err == nil {
			t.Fatalf("-export_port=%d 应被拒绝", p)
		}
		if !strings.Contains(err.Error(), "-export_port") {
			t.Fatalf("错误应指明是 -export_port 的问题, got %v", err)
		}
	}
	for _, p := range []uint{1, 65535} { // 边界合法
		cfg := baseConfig()
		cfg.exportPort = p
		if err := tryValidate(cfg); err != nil {
			t.Fatalf("-export_port=%d 应合法: %v", p, err)
		}
	}
}

// 回归 M-19：缺冒号的地址必须被拒（Readme 旧示例 `-listen=8888` 的同类问题）。
func TestValidate_RejectsBadAddresses(t *testing.T) {
	for _, s := range []string{"8888", "", "host", "h:0", "h:65536", "h:abc", "::1", "a:b:c"} {
		cfg := baseConfig()
		cfg.server = s
		err := tryValidate(cfg)
		if err == nil {
			t.Fatalf("-server=%q 应被拒绝", s)
		}
		if !strings.Contains(err.Error(), "-server") {
			t.Fatalf("-server=%q 的错误应指明 -server, got %v", s, err)
		}
	}
	for _, f := range []string{"3306", "", "h:0", "h:xyz"} {
		cfg := baseConfig()
		cfg.forward = f
		err := tryValidate(cfg)
		if err == nil {
			t.Fatalf("-forward=%q 应被拒绝", f)
		}
		if !strings.Contains(err.Error(), "-forward") {
			t.Fatalf("-forward=%q 的错误应指明 -forward, got %v", f, err)
		}
	}
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	for _, lvl := range []int{-1, 6, 100} {
		cfg := baseConfig()
		cfg.logLevel = lvl
		if err := tryValidate(cfg); err == nil {
			t.Fatalf("-log_level=%d 应被拒绝", lvl)
		}
	}
	for _, lvl := range []int{0, 5} { // 边界合法
		cfg := baseConfig()
		cfg.logLevel = lvl
		if err := tryValidate(cfg); err != nil {
			t.Fatalf("-log_level=%d 应合法: %v", lvl, err)
		}
	}
}

// 回归 DOS-01（入口侧）：拨号与握手超时必须可配且必须为正数。
// 0 会让拨号立刻失败或让握手无限等待，等于没修。
func TestValidate_RejectsNonPositiveTimeouts(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*config)
		wantSub string
	}{
		{"dial_timeout=0", func(c *config) { c.dialTimeout = 0 }, "-dial_timeout"},
		{"dial_timeout 负数", func(c *config) { c.dialTimeout = -time.Second }, "-dial_timeout"},
		{"handshake_timeout=0", func(c *config) { c.handshakeTo = 0 }, "-handshake_timeout"},
		{"handshake_timeout 负数", func(c *config) { c.handshakeTo = -time.Millisecond }, "-handshake_timeout"},
	}
	for _, tc := range cases {
		cfg := baseConfig()
		tc.mut(&cfg)
		err := tryValidate(cfg)
		if err == nil {
			t.Fatalf("%s 应被拒绝", tc.name)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("%s 的错误应指明 %s, got %v", tc.name, tc.wantSub, err)
		}
	}
}

// 配置必须真的能覆盖 engine 侧默认值，否则入口参数是摆设。
func TestTimeoutOverrides_AreEffective(t *testing.T) {
	cfg, err := parseFlags([]string{"-dial_timeout=1s", "-handshake_timeout=2s"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	prevDial, prevHS := socketNetConnect.DialTimeout, netClient.HandshakeTimeout
	t.Cleanup(func() {
		socketNetConnect.DialTimeout = prevDial
		netClient.HandshakeTimeout = prevHS
	})

	// 与 run() 里的应用逻辑一致（run 之后会落盘并进入无限重连，不适合在单测跑完）
	socketNetConnect.DialTimeout = cfg.dialTimeout
	netClient.HandshakeTimeout = cfg.handshakeTo

	if socketNetConnect.DialTimeout != time.Second {
		t.Fatalf("DialTimeout 未被覆盖, got %v", socketNetConnect.DialTimeout)
	}
	if netClient.HandshakeTimeout != 2*time.Second {
		t.Fatalf("HandshakeTimeout 未被覆盖, got %v", netClient.HandshakeTimeout)
	}
}

// validate 的错误必须指到具体参数名，否则运维无从下手。
func TestValidate_ErrorMessagesNameTheFlag(t *testing.T) {
	cfg := baseConfig()
	cfg.server = "8888"
	if err := tryValidate(cfg); err == nil || !strings.Contains(err.Error(), "-server") {
		t.Fatalf("错误应指明 -server, got %v", err)
	}

	cfg = baseConfig()
	cfg.forward = "3306"
	if err := tryValidate(cfg); err == nil || !strings.Contains(err.Error(), "-forward") {
		t.Fatalf("错误应指明 -forward, got %v", err)
	}
}

// 重连间隔必须为正且可被测试调整（避免集成用例真等 10 秒）。
func TestReconnectDelay_IsPositiveAndTestAdjustable(t *testing.T) {
	if reconnectDelay <= 0 {
		t.Fatalf("重连间隔应为正数, got %v", reconnectDelay)
	}
	old := reconnectDelay
	t.Cleanup(func() { reconnectDelay = old })
	reconnectDelay = 5 * time.Millisecond
	if reconnectDelay != 5*time.Millisecond {
		t.Fatal("reconnectDelay 应可被测试调整")
	}
}
