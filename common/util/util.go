package util

import (
	"fmt"
	"strconv"
	"strings"
)

func GetHostAndPort(address string) (string, uint16, error) {
	// 取最后一个 ':' 分隔主机与端口，兼容 IPv6 字面量（如 "[::1]:8888"）。
	// 返回的 host 保留方括号，调用方以 host+":"+port 拼接后可直接用于 net.Dial/Listen。
	idx := strings.LastIndex(address, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("invalid host")
	}
	port, err := strconv.Atoi(address[idx+1:])
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid host")
	}
	return address[:idx], uint16(port), nil
}
