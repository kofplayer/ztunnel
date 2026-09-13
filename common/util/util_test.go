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

// 回归：应支持 IPv6 字面量。host 以**不带方括号**的裸主机返回，
// 调用方必须用 net.JoinHostPort 拼接，不得手写 host+":"+port。
func TestGetHostAndPort_IPv6(t *testing.T) {
	host, port, err := GetHostAndPort("[::1]:8888")
	testutil.NoError(t, err, "回归未修复：应支持 IPv6 地址 [::1]:8888")
	testutil.Equal(t, "::1", host)
	testutil.Equal(t, uint16(8888), port)
}

// 回归：未加方括号的 IPv6 与多冒号地址必须明确报错，而不是静默误解析
// （此前 "::1" 会被拆成 host="::"、port=1，拨号地址与真实原因完全无关）。
func TestGetHostAndPort_RejectsAmbiguous(t *testing.T) {
	for _, in := range []string{"::1", "10.0.0.1:33:80", "[::1]", "2001:db8::1:8080"} {
		_, _, err := GetHostAndPort(in)
		testutil.Error(t, err, "应拒绝歧义地址", in)
	}
}
