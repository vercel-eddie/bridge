//go:build !linux

package commands

import (
	"fmt"
	"net"
)

func getOriginalDst(_ net.Conn) (string, error) {
	return "", fmt.Errorf("SO_ORIGINAL_DST not supported on this platform")
}
