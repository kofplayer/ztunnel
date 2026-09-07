package util

import (
	"testing"

	"ztunnel/testutil"
)

func TestGetHostAndPort(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port uint16
		ok   bool
	}{
		{"localhost:8888", "localhost", 8888, true},
		{":8888", "", 8888, true},
		{"192.168.0.100:3306", "192.168.0.100", 3306, true},
		{"host", "", 0, false},
		{"a:b:c", "", 0, false},
		{"host:0", "", 0, false},
		{"host:65536", "", 0, false},
		{"host:-1", "", 0, false},
		{"host:abc", "", 0, false},
	}
	for _, tc := range cases {
		host, port, err := GetHostAndPort(tc.in)
		if tc.ok {
			testutil.NoError(t, err, "input", tc.in)
			testutil.Equal(t, tc.host, host, "input", tc.in)
			testutil.Equal(t, tc.port, port, "input", tc.in)
		} else {
			testutil.Error(t, err, "input", tc.in)
		}
	}
}

// 回归：应支持 IPv6 字面量。host 保留方括号返回，保证 host+":"+port 可直接用于 net.Dial。
func TestGetHostAndPort_IPv6(t *testing.T) {
	host, port, err := GetHostAndPort("[::1]:8888")
	testutil.NoError(t, err, "回归未修复：应支持 IPv6 地址 [::1]:8888")
	testutil.Equal(t, "[::1]", host)
	testutil.Equal(t, uint16(8888), port)
}
