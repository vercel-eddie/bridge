package e2e

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

// TestNetwork represents a Docker network for e2e tests
type TestNetwork struct {
	Network *testcontainers.DockerNetwork
	Name    string
}

// NewTestNetwork creates a new Docker network for container-to-container communication
func NewTestNetwork(ctx context.Context, name string) (*TestNetwork, error) {
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return &TestNetwork{
		Network: net,
		Name:    net.Name,
	}, nil
}

// Terminate removes the network
func (n *TestNetwork) Terminate(ctx context.Context) error {
	if n.Network != nil {
		return n.Network.Remove(ctx)
	}
	return nil
}
