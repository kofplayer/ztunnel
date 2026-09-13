package util

import (
	"fmt"
	"net"
	"strconv"
)

// GetHostAndPort 拆分 host:port。返回的 host 为**不带方括号**的裸主机
// （"[::1]:8888" → "::1"），调用方拼接地址时必须用 net.JoinHostPort。
func GetHostAndPort(address string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port in %q", address)
	}
	return host, uint16(port), nil
}
