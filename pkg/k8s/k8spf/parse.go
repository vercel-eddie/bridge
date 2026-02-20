package k8spf

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc/resolver"
)

// Target holds the parsed components of a k8spf:/// address.
type Target struct {
	Name      string // pod or deployment name depending on Workload
	Namespace string
	Port      int
	Workload  string // "pod" (default) or "deployment"
	Context   string // optional kubectl context override
}

// ParseTarget extracts a Target from a gRPC resolver.Target.
// Expected endpoint format: "name.namespace:port[?workload=deployment]"
// Note: resolver.Target.Endpoint() strips query parameters, so we
// reconstruct the full address from the URL path and raw query.
func ParseTarget(t resolver.Target) (Target, error) {
	addr := t.Endpoint()
	if q := t.URL.RawQuery; q != "" {
		addr += "?" + q
	}
	return ParseAddr(addr)
}

// ParseAddr parses an address into a Target. It accepts both the full URI form
// "k8spf:///pod.namespace:port[?context=ctx]" and the bare
// "pod.namespace:port[?context=ctx]" form.
func ParseAddr(addr string) (Target, error) {
	// Strip the scheme prefix if present.
	if strings.HasPrefix(addr, Scheme+":///") {
		addr = strings.TrimPrefix(addr, Scheme+":///")
	} else if strings.HasPrefix(addr, Scheme+"://") {
		addr = strings.TrimPrefix(addr, Scheme+"://")
	}

	// Separate query parameters if present.
	var query url.Values
	if i := strings.IndexByte(addr, '?'); i >= 0 {
		var err error
		query, err = url.ParseQuery(addr[i+1:])
		if err != nil {
			return Target{}, fmt.Errorf("k8spf: parse query %q: %w", addr, err)
		}
		addr = addr[:i]
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return Target{}, fmt.Errorf("k8spf: split host/port %q: %w", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Target{}, fmt.Errorf("k8spf: invalid port %q", portStr)
	}

	// Reject IP addresses — they should be dialed as plain TCP.
	if net.ParseIP(host) != nil {
		return Target{}, fmt.Errorf("k8spf: address %q is an IP address, not pod.namespace", host)
	}

	dot := strings.IndexByte(host, '.')
	if dot < 0 || dot == 0 || dot == len(host)-1 {
		return Target{}, fmt.Errorf("k8spf: address %q must be pod.namespace", host)
	}

	name := host[:dot]
	ns := host[dot+1:]

	workload := query.Get("workload")
	if workload == "" {
		workload = "pod"
	}
	if workload != "pod" && workload != "deployment" {
		return Target{}, fmt.Errorf("k8spf: invalid workload %q, must be \"pod\" or \"deployment\"", workload)
	}

	return Target{
		Name:      name,
		Namespace: ns,
		Port:      port,
		Workload:  workload,
		Context:   query.Get("context"),
	}, nil
}

// String returns the canonical "name.namespace:port" representation,
// appending "?workload=deployment" when the target refers to a deployment.
func (t Target) String() string {
	s := net.JoinHostPort(t.Name+"."+t.Namespace, strconv.Itoa(t.Port))
	if t.Workload == "deployment" {
		s += "?workload=deployment"
	}
	return s
}
