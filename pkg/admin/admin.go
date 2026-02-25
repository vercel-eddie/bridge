// Package admin abstracts bridge administration operations. Implementations
// include a remote gRPC client (connecting to an in-cluster administrator)
// and a local implementation that performs operations directly via kubeconfig.
package admin

import (
	"context"

	bridgev1 "github.com/vercel/bridge/api/go/bridge/v1"
)

// Service is the interface for bridge administration operations.
type Service interface {
	CreateBridge(ctx context.Context, in *bridgev1.CreateBridgeRequest) (*bridgev1.CreateBridgeResponse, error)
	// ListBridges returns all active bridges for a device across all namespaces.
	ListBridges(ctx context.Context, in *bridgev1.ListBridgesRequest) (*bridgev1.ListBridgesResponse, error)
	// DeleteBridge tears down a specific bridge and its associated resources.
	DeleteBridge(ctx context.Context, in *bridgev1.DeleteBridgeRequest) (*bridgev1.DeleteBridgeResponse, error)
}
