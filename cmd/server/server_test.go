package main

import (
	"errors"
	"strings"
	"testing"
)

// 入口的校验逻辑此前完全无测试（cmd/* 0%）——而它恰好是用户体验问题最集中的地方：
// 配置写错要么静默失败、要么把端口改成一个谁都没预期的值。

func TestParseFlags_Defaults(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("空参数应全部走默认值: %v", err)
	}
	if cfg.listen != ":8888" {
		t.Fatalf("-listen 默认值应为 :8888, got %q", cfg.listen)
	}
	if cfg.exportIP != "" || cfg.token != "" {
		t.Fatalf("-export_ip/-token 默认应为空, got %q/%q", cfg.exportIP, cfg.token)
	}
	if cfg.netEncrypt {
		t.Fatal("-net_encrypt 默认应为 false")
	}
	if cfg.logLevel != 0 {
		t.Fatalf("-log_level 默认应为 DEBUG(0), got %d", cfg.logLevel)
	}
}

func TestParseFlags_AllFlags(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-listen=127.0.0.1:9000", "-export_ip=10.0.0.1",
		"-token=s3cr3t", "-net_encrypt=true", "-log_level=3",
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.listen != "127.0.0.1:9000" || cfg.exportIP != "10.0.0.1" ||
		cfg.token != "s3cr3t" || !cfg.netEncrypt || cfg.logLevel != 3 {
		t.Fatalf("参数解析结果不符: %+v", cfg)
	}
}

func TestParseFlags_UnknownFlagAndBadInt(t *testing.T) {
	if _, err := parseFlags([]string{"-nope"}); err == nil {
		t.Fatal("未知参数必须报错")
	}
	if _, err := parseFlags([]string{"-log_level=abc"}); err == nil {
		t.Fatal("非整数 log_level 必须报错")
	}
}

func TestParseFlags_Help(t *testing.T) {
	// -h 走 ErrHelp 分支：main 据此以退出码 0 结束（与 Go flag 包惯例一致）
	_, err := parseFlags([]string{"-h"})
	if !errors.Is(err, errHelp) {
		t.Fatalf("-h 应返回 errHelp, got %v", err)
	}
}

func TestValidate_OK(t *testing.T) {
	cases := []struct {
		listen, exportIP, wantHost string
		wantPort                   uint16
	}{
		{":8888", "", "", 8888},
		{"127.0.0.1:8888", "", "127.0.0.1", 8888},
		{"[::1]:8888", "", "::1", 8888},    // IPv6 字面量：host 不带方括号返回
		{":9000", "10.1.2.3", "", 9000},    // 指定 export 绑定地址
		{":9000", "2001:db8::1", "", 9000}, // IPv6 export 地址
	}
	for _, tc := range cases {
		cfg := config{logLevel: 0, listen: tc.listen, exportIP: tc.exportIP}
		got, err := cfg.validate()
		if err != nil {
			t.Fatalf("validate(%q,%q) 意外报错: %v", tc.listen, tc.exportIP, err)
		}
		if got.host != tc.wantHost || got.port != tc.wantPort {
			t.Fatalf("validate(%q) = %q:%d, 期望 %q:%d",
				tc.listen, got.host, got.port, tc.wantHost, tc.wantPort)
		}
		if got.exportIP != tc.exportIP {
			t.Fatalf("exportIP 应原样透传, got %q", got.exportIP)
		}
	}
}

// 这些写法在修复前要么静默失败、要么以退出码 0 结束，照 Readme 操作会以为服务起来了。
func TestValidate_RejectsBadListen(t *testing.T) {
	// 注意 "8888"：Readme 曾经的错误示例，必须明确拒绝
	for _, bad := range []string{"8888", "host", "", ":0", ":65536", ":abc", "1.2.3.4:100000", "a:b:c"} {
		cfg := config{listen: bad}
		if _, err := cfg.validate(); err == nil {
			t.Fatalf("-listen=%q 应被拒绝", bad)
		}
	}
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	for _, lvl := range []int{-1, 6, 999} {
		cfg := config{logLevel: lvl, listen: ":8888"}
		if _, err := cfg.validate(); err == nil {
			t.Fatalf("-log_level=%d 应被拒绝（合法区间 0-5）", lvl)
		}
	}
	// 边界合法
	for _, lvl := range []int{0, 5} {
		cfg := config{logLevel: lvl, listen: ":8888"}
		if _, err := cfg.validate(); err != nil {
			t.Fatalf("-log_level=%d 应合法: %v", lvl, err)
		}
	}
}

// -export_ip 只接受不带端口的裸 IP，避免和 -listen 的格式混淆。
func TestSplitOptionalIP(t *testing.T) {
	ok := map[string]string{"": "", "10.0.0.1": "10.0.0.1", "2001:db8::1": "2001:db8::1"}
	for in, want := range ok {
		got, err := splitOptionalIP(in)
		if err != nil {
			t.Fatalf("splitOptionalIP(%q) 意外报错: %v", in, err)
		}
		if got != want {
			t.Fatalf("splitOptionalIP(%q) = %q, 期望 %q", in, got, want)
		}
	}

	for _, bad := range []string{"1.2.3.4.5", "10.0.0.1:8080", "localhost", "not-an-ip", "[::1]:8080"} {
		if _, err := splitOptionalIP(bad); err == nil {
			t.Fatalf("splitOptionalIP(%q) 应被拒绝", bad)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("错误信息不应为空")
		}
	}
}

// validate 的错误信息必须能指到具体参数，否则运维无从下手。
func TestValidate_ErrorMessagesNameTheFlag(t *testing.T) {
	_, err := (config{listen: "8888"}).validate()
	if err == nil || !strings.Contains(err.Error(), "-listen") {
		t.Fatalf("错误应指明是 -listen 的问题, got %v", err)
	}
	_, err = (config{listen: ":8888", exportIP: "1.2.3.4.5"}).validate()
	if err == nil || !strings.Contains(err.Error(), "-export_ip") {
		t.Fatalf("错误应指明是 -export_ip 的问题, got %v", err)
	}
	_, err = (config{listen: ":8888", logLevel: 9}).validate()
	if err == nil || !strings.Contains(err.Error(), "-log_level") {
		t.Fatalf("错误应指明是 -log_level 的问题, got %v", err)
	}
}
