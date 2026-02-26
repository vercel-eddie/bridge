package admin

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bridgev1 "github.com/vercel/bridge/api/go/bridge/v1"
	"github.com/vercel/bridge/pkg/identity"
	"github.com/vercel/bridge/pkg/k8s/kube"
	"github.com/vercel/bridge/pkg/k8s/meta"
	"github.com/vercel/bridge/pkg/k8s/namespace"
	"github.com/vercel/bridge/pkg/k8s/portforward"
	"github.com/vercel/bridge/pkg/k8s/resources"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const defaultProxyImage = "ghcr.io/vercel/bridge-cli:latest"

// LocalConfig configures the local admin implementation.
type LocalConfig struct {
	// ProxyImage is the container image for the bridge proxy pod.
	// Defaults to ghcr.io/vercel/bridge-cli:latest.
	ProxyImage string
	// ServiceAccountName is the administrator's SA name for namespace RBAC.
	// Defaults to "administrator".
	ServiceAccountName string
	// ServiceAccountNamespace is the namespace of the administrator's SA.
	// Defaults to "bridge".
	ServiceAccountNamespace string
}

var _ Service = (*adminService)(nil)

// adminService implements Service by performing operations directly against the
// Kubernetes API using the user's local kubeconfig credentials.
type adminService struct {
	client     kubernetes.Interface
	restConfig *rest.Config
	config     LocalConfig
}

// NewService creates a local Service that performs operations using the current
// kubeconfig context.
func NewService(cfg LocalConfig) (Service, error) {
	restCfg, err := kube.RestConfig(kube.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return NewLocalFromClient(clientset, restCfg, cfg), nil
}

// NewLocalFromClient creates a local Service from an existing Kubernetes client.
// Used by the administrator server to share the same client.
func NewLocalFromClient(client kubernetes.Interface, restCfg *rest.Config, cfg LocalConfig) Service {
	if cfg.ProxyImage == "" {
		cfg.ProxyImage = defaultProxyImage
	}
	if cfg.ServiceAccountName == "" {
		cfg.ServiceAccountName = "administrator"
	}
	if cfg.ServiceAccountNamespace == "" {
		cfg.ServiceAccountNamespace = "bridge"
	}
	return &adminService{
		client:     client,
		restConfig: restCfg,
		config:     cfg,
	}
}

func (l *adminService) CreateBridge(ctx context.Context, req *bridgev1.CreateBridgeRequest) (*bridgev1.CreateBridgeResponse, error) {
	if req.DeviceId == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	if req.SourceDeployment != "" && req.SourceNamespace == "" {
		return nil, fmt.Errorf("source_namespace is required when source_deployment is set")
	}

	logger := slog.With("device_id", req.DeviceId)

	var result *resources.CopyResult
	var targetNS string

	if req.SourceDeployment != "" {
		targetNS = req.SourceNamespace
		bridgeName := identity.BridgeResourceName(req.DeviceId, req.SourceDeployment)
		logger = logger.With("namespace", targetNS, "bridge", bridgeName)
		logger.Info("Creating bridge")

		// Tear down existing bridge if force is set.
		if req.Force {
			if _, err := l.client.AppsV1().Deployments(targetNS).Get(ctx, bridgeName, metav1.GetOptions{}); err == nil {
				logger.Info("Tearing down existing bridge")
				_ = resources.DeleteBridgeResources(ctx, l.client, targetNS, bridgeName)
			}
		}

		var err error
		result, err = resources.CreateInNamespace(ctx, l.client, resources.InNamespaceConfig{
			SourceNamespace:  req.SourceNamespace,
			SourceDeployment: req.SourceDeployment,
			DeviceID:         req.DeviceId,
			ProxyImage:       l.config.ProxyImage,
		})
		if err != nil {
			return nil, err
		}
	} else {
		// No source deployment — fall back to device namespace with simple deployment.
		targetNS = identity.NamespaceForDevice(req.DeviceId)
		logger = logger.With("namespace", targetNS)
		logger.Info("Creating simple bridge")

		if err := namespace.EnsureNamespace(ctx, l.client, namespace.CreateConfig{
			Name:                    targetNS,
			DeviceID:                req.DeviceId,
			ServiceAccountName:      l.config.ServiceAccountName,
			ServiceAccountNamespace: l.config.ServiceAccountNamespace,
		}); err != nil {
			return nil, fmt.Errorf("failed to ensure namespace: %w", err)
		}
		var err error
		result, err = resources.CreateSimpleDeployment(ctx, l.client, targetNS, l.config.ProxyImage)
		if err != nil {
			return nil, err
		}
	}

	// Wait for the pod to be ready.
	podName, err := kube.WaitForPod(ctx, l.client, targetNS, meta.DeploymentSelector(result.DeploymentName), 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed waiting for pod: %w", err)
	}

	// Fetch environment variables from the proxy pod via port-forward.
	var envVars map[string]string
	pod, err := l.client.CoreV1().Pods(targetNS).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		logger.Warn("Failed to get pod for metadata", "pod", podName, "error", err)
	} else if pod.Status.PodIP != "" {
		if md, err := l.fetchProxyMetadata(ctx, targetNS, podName, int(result.PodPort)); err != nil {
			logger.Warn("GetMetadata call failed", "pod", podName, "error", err)
		} else {
			envVars = md
		}
	}

	return &bridgev1.CreateBridgeResponse{
		Namespace:        targetNS,
		PodName:          podName,
		Port:             result.PodPort,
		DeploymentName:   result.DeploymentName,
		EnvVars:          envVars,
		VolumeMountPaths: result.VolumeMountPaths,
		AppPorts:         result.AppPorts,
	}, nil
}

func (l *adminService) ListBridges(ctx context.Context, req *bridgev1.ListBridgesRequest) (*bridgev1.ListBridgesResponse, error) {
	if req.DeviceId == "" {
		return nil, fmt.Errorf("device_id is required")
	}

	// List bridge deployments across all namespaces for this device.
	deploys, err := l.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: meta.DeviceSelector(req.DeviceId),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list bridge deployments: %w", err)
	}

	var bridges []*bridgev1.BridgeInfo
	for _, d := range deploys.Items {
		bridges = append(bridges, &bridgev1.BridgeInfo{
			DeviceId:         req.DeviceId,
			SourceDeployment: d.Labels[meta.LabelWorkloadSource],
			SourceNamespace:  d.Labels[meta.LabelWorkloadSourceNamespace],
			Namespace:        d.Namespace,
			DeploymentName:   d.Name,
			CreatedAt:        d.CreationTimestamp.Format(time.RFC3339),
		})
	}

	return &bridgev1.ListBridgesResponse{Bridges: bridges}, nil
}

func (l *adminService) DeleteBridge(ctx context.Context, req *bridgev1.DeleteBridgeRequest) (*bridgev1.DeleteBridgeResponse, error) {
	if req.DeviceId == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	slog.Info("Deleting bridge", "device_id", req.DeviceId, "namespace", req.Namespace, "name", req.Name)

	if err := resources.DeleteBridgeResources(ctx, l.client, req.Namespace, req.Name); err != nil {
		return nil, err
	}

	return &bridgev1.DeleteBridgeResponse{}, nil
}

// Close releases resources. No-op for local admin.
func (l *adminService) Close() error {
	return nil
}

func (l *adminService) fetchProxyMetadata(ctx context.Context, ns, podName string, port int) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer, err := portforward.NewDialer(l.restConfig, l.client, ns, podName, port)
	if err != nil {
		return nil, fmt.Errorf("create port-forward dialer: %w", err)
	}

	conn, err := grpc.NewClient("passthrough:///pod",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer.DialContext),
	)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}
	defer conn.Close()

	client := bridgev1.NewBridgeProxyServiceClient(conn)
	resp, err := client.GetMetadata(ctx, &bridgev1.GetMetadataRequest{})
	if err != nil {
		return nil, fmt.Errorf("GetMetadata: %w", err)
	}
	return resp.GetEnvVars(), nil
}
