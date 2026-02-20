//go:build !linux

package netutil

import (
	"fmt"
	"net"
)

// OriginalDest returns the original destination of the connection. On
// non-Linux platforms SO_ORIGINAL_DST is not available, but there are no
// iptables redirects either, so RemoteAddr is the true destination.
func OriginalDest(conn net.Conn) (string, error) {
	switch addr := conn.RemoteAddr().(type) {
	case *net.TCPAddr:
		return addr.String(), nil
	case *net.UDPAddr:
		return addr.String(), nil
	default:
		return "", fmt.Errorf("unsupported address type %T", addr)
	}
}
