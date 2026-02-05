//go:build linux

package commands

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const soOriginalDst = 80

type sockaddrIn struct {
	Family uint16
	Port   [2]byte
	Addr   [4]byte
	Zero   [8]byte
}

func getOriginalDst(conn net.Conn) (string, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not a TCP connection")
	}

	file, err := tcpConn.File()
	if err != nil {
		return "", err
	}
	defer file.Close()

	fd := int(file.Fd())

	var addr sockaddrIn
	addrLen := uint32(unsafe.Sizeof(addr))

	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(syscall.IPPROTO_IP),
		uintptr(soOriginalDst),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Pointer(&addrLen)),
		0,
	)

	if errno != 0 {
		return "", fmt.Errorf("getsockopt SO_ORIGINAL_DST failed: %v", errno)
	}

	// Port is in network byte order (big endian)
	port := int(addr.Port[0])<<8 + int(addr.Port[1])
	ip := net.IPv4(addr.Addr[0], addr.Addr[1], addr.Addr[2], addr.Addr[3])

	return fmt.Sprintf("%s:%d", ip, port), nil
}
