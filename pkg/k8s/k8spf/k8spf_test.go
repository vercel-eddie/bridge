package k8spf

import (
	"testing"

	"google.golang.org/grpc/resolver"
)

func TestParseAddr(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    Target
		wantErr bool
	}{
		{
			name: "basic",
			addr: "admin-pod.bridge:9090",
			want: Target{Name: "admin-pod", Namespace: "bridge", Port: 9090, Workload: "pod"},
		},
		{
			name: "with context query",
			addr: "admin-pod.bridge:9090?context=staging",
			want: Target{Name: "admin-pod", Namespace: "bridge", Port: 9090, Workload: "pod", Context: "staging"},
		},
		{
			name: "dotted namespace",
			addr: "pod.ns-1:443",
			want: Target{Name: "pod", Namespace: "ns-1", Port: 443, Workload: "pod"},
		},
		{
			name: "high port",
			addr: "p.ns:65535",
			want: Target{Name: "p", Namespace: "ns", Port: 65535, Workload: "pod"},
		},
		{
			name: "workload deployment",
			addr: "deploy.ns:9090?workload=deployment",
			want: Target{Name: "deploy", Namespace: "ns", Port: 9090, Workload: "deployment"},
		},
		{
			name: "explicit workload pod",
			addr: "pod.ns:9090?workload=pod",
			want: Target{Name: "pod", Namespace: "ns", Port: 9090, Workload: "pod"},
		},
		{
			name: "workload deployment with context",
			addr: "deploy.ns:9090?workload=deployment&context=staging",
			want: Target{Name: "deploy", Namespace: "ns", Port: 9090, Workload: "deployment", Context: "staging"},
		},
		{
			name: "full URI with workload deployment",
			addr: "k8spf:///bridge-administrator.bridge:9090?workload=deployment",
			want: Target{Name: "bridge-administrator", Namespace: "bridge", Port: 9090, Workload: "deployment"},
		},
		{
			name:    "invalid workload",
			addr:    "pod.ns:9090?workload=invalid",
			wantErr: true,
		},
		{
			name:    "missing namespace",
			addr:    "pod:9090",
			wantErr: true,
		},
		{
			name:    "empty pod",
			addr:    ".ns:9090",
			wantErr: true,
		},
		{
			name:    "empty namespace",
			addr:    "pod.:9090",
			wantErr: true,
		},
		{
			name:    "missing port",
			addr:    "pod.ns",
			wantErr: true,
		},
		{
			name:    "zero port",
			addr:    "pod.ns:0",
			wantErr: true,
		},
		{
			name:    "negative port",
			addr:    "pod.ns:-1",
			wantErr: true,
		},
		{
			name:    "port too high",
			addr:    "pod.ns:99999",
			wantErr: true,
		},
		{
			name:    "ipv4 address",
			addr:    "172.29.0.2:9090",
			wantErr: true,
		},
		{
			name:    "ipv6 address",
			addr:    "[::1]:9090",
			wantErr: true,
		},
		{
			name:    "empty string",
			addr:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAddr(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseAddr(%q) succeeded, want error", tt.addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAddr(%q) error: %v", tt.addr, err)
			}
			if got != tt.want {
				t.Errorf("ParseAddr(%q) = %+v, want %+v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	t.Run("bare endpoint", func(t *testing.T) {
		target := resolver.Target{}
		target.URL.Scheme = Scheme
		target.URL.Path = "/admin-pod.bridge:9090"

		got, err := ParseTarget(target)
		if err != nil {
			t.Fatalf("ParseTarget error: %v", err)
		}
		want := Target{Name: "admin-pod", Namespace: "bridge", Port: 9090, Workload: "pod"}
		if got != want {
			t.Errorf("ParseTarget = %+v, want %+v", got, want)
		}
	})

	t.Run("with query params", func(t *testing.T) {
		// gRPC puts query params in URL.RawQuery, not in the path.
		target := resolver.Target{}
		target.URL.Scheme = Scheme
		target.URL.Path = "/administrator.bridge:9090"
		target.URL.RawQuery = "workload=deployment"

		got, err := ParseTarget(target)
		if err != nil {
			t.Fatalf("ParseTarget error: %v", err)
		}
		want := Target{Name: "administrator", Namespace: "bridge", Port: 9090, Workload: "deployment"}
		if got != want {
			t.Errorf("ParseTarget = %+v, want %+v", got, want)
		}
	})
}

func TestTargetString(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{
			name:   "pod workload",
			target: Target{Name: "admin-pod", Namespace: "bridge", Port: 9090, Workload: "pod"},
			want:   "admin-pod.bridge:9090",
		},
		{
			name:   "default workload omits query",
			target: Target{Name: "admin-pod", Namespace: "bridge", Port: 9090},
			want:   "admin-pod.bridge:9090",
		},
		{
			name:   "deployment workload includes query",
			target: Target{Name: "bridge-administrator", Namespace: "bridge", Port: 9090, Workload: "deployment"},
			want:   "bridge-administrator.bridge:9090?workload=deployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.target.String(); got != tt.want {
				t.Errorf("Target.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
