package tunnel

import (
	"context"
	"net"
)

// DNSResolveResult holds the result of a DNS resolution.
type DNSResolveResult struct {
	Addresses []string
	Error     string
}

// TunnelDialer abstracts the transport used by the DNS and proxy components
// to resolve hostnames and dial upstream connections. The WebSocket-based
// tunnel.Client satisfies this interface.
type TunnelDialer interface {
	ResolveDNS(ctx context.Context, hostname string) (*DNSResolveResult, error)
	DialThroughTunnel(sourceAddr, destination string) (net.Conn, error)
	Close() error
}
