package util

import (
	"fmt"
	"strconv"
	"strings"
)

func GetHostAndPort(address string) (string, uint16, error) {
	ss := strings.Split(address, ":")
	if len(ss) != 2 {
		return "", 0, fmt.Errorf("invalid host")
	}
	port, err := strconv.Atoi(ss[1])
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid host")
	}
	return ss[0], uint16(port), nil
}
