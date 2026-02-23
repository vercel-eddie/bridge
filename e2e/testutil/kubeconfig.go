package testutil

import (
	"fmt"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// RewriteKubeconfigServer rewrites the server URL in a kubeconfig for use
// inside the Docker network. It sets insecure-skip-tls-verify since the
// k3s TLS cert won't have the Docker network alias as a SAN.
func RewriteKubeconfigServer(kubeconfig []byte, newServerURL string) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	for _, cluster := range cfg.Clusters {
		cluster.Server = newServerURL
		cluster.InsecureSkipTLSVerify = true
		cluster.CertificateAuthorityData = nil
	}

	return clientcmd.Write(*cfg)
}

// ClientsetFromKubeconfig creates a kubernetes.Interface from raw kubeconfig bytes.
func ClientsetFromKubeconfig(kubeconfig []byte) (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	return cfg, nil
}
