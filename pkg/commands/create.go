package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/vercel-eddie/bridge/api/go/bridge/v1"
	"github.com/vercel-eddie/bridge/pkg/devcontainer"
	"github.com/vercel-eddie/bridge/pkg/identity"
	"github.com/vercel-eddie/bridge/pkg/k8s/k8spf"
)

const defaultFeatureRef = "ghcr.io/vercel-eddie/bridge/features/bridge:edge"

const defaultAdminAddr = "k8spf:///administrator.bridge:9090?workload=deployment"

// Create returns the CLI command for creating a bridge.
func Create() *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a bridge to a Kubernetes deployment",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "connect",
				Usage: "Start a Devcontainer and connect to the bridge after creation",
			},
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Source namespace of the deployment",
				Sources: cli.EnvVars("BRIDGE_SOURCE_NAMESPACE"),
			},
			&cli.StringFlag{
				Name:    "admin-addr",
				Usage:   "Address of the bridge administrator (e.g. localhost:9090 or k8spf:///pod.ns:9090)",
				Value:   defaultAdminAddr,
				Sources: cli.EnvVars("BRIDGE_ADMIN_ADDR"),
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Force recreation without confirmation",
			},
			&cli.StringFlag{
				Name:    "devcontainer-config",
				Aliases: []string{"f"},
				Usage:   "Path to the base devcontainer.json config file",
			},
			&cli.IntFlag{
				Name:    "listen",
				Aliases: []string{"l"},
				Usage:   "App listening port to forward inbound requests to",
				Value:   3000,
			},
			&cli.StringFlag{
				Name:    "feature-ref",
				Usage:   "Devcontainer feature reference for the bridge feature",
				Value:   defaultFeatureRef,
				Hidden:  true,
				Sources: cli.EnvVars("BRIDGE_FEATURE_REF"),
			},
		},
		Arguments: []cli.Argument{
			&cli.StringArg{
				Name:      "deployment",
				UsageText: "Name of the source Deployment to bridge (optional)",
				Config: cli.StringConfig{
					TrimSpace: true,
				},
			},
		},
		Action: runCreate,
	}
}

func runCreate(ctx context.Context, c *cli.Command) error {
	deploymentName := c.StringArg("deployment")
	sourceNamespace := c.String("namespace")
	adminAddr := c.String("admin-addr")
	connectFlag := c.Bool("connect")
	force := c.Bool("force")
	featureRef := c.String("feature-ref")

	w := c.Root().Writer
	r := c.Root().Reader

	// Step 1: Resolve device identity
	deviceID, err := identity.GetDeviceID()
	if err != nil {
		return fmt.Errorf("failed to get device identity: %w", err)
	}
	slog.Info("Device identity", "device_id", deviceID)

	// Step 2: Connect to the administrator
	slog.Info("Connecting to bridge administrator...", "addr", adminAddr)

	builder := k8spf.NewBuilder(k8spf.BuilderConfig{})
	conn, err := grpc.NewClient(adminAddr,
		append(builder.DialOptions(), grpc.WithTransportCredentials(insecure.NewCredentials()))...,
	)
	if err != nil {
		return fmt.Errorf("failed to connect to administrator: %w", err)
	}
	defer conn.Close()

	client := pb.NewAdministratorServiceClient(conn)

	// Step 4: Check for existing bridges
	listResp, err := client.ListBridges(ctx, &pb.ListBridgesRequest{
		DeviceId: deviceID,
	})
	if err != nil {
		slog.Warn("Failed to list existing bridges", "error", err)
	} else if len(listResp.Bridges) > 0 && !force {
		// Check if there's an existing bridge for the same deployment
		for _, bridge := range listResp.Bridges {
			if bridge.SourceDeployment == deploymentName {
				fmt.Fprintf(w, "\nWarning: An existing bridge for deployment %q already exists in namespace %q (created %s).\n",
					bridge.SourceDeployment, bridge.Namespace, bridge.CreatedAt)
				fmt.Fprintf(w, "This will tear down the existing bridge and recreate it.\n")
				fmt.Fprintf(w, "Continue? [y/N] ")

				reader := bufio.NewReader(r)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Fprintf(w, "Aborted.\n")
					return nil
				}
				force = true
				break
			}
		}
	}

	// Step 5: Create the bridge
	slog.Info("Creating bridge...",
		"deployment", deploymentName,
		"source_namespace", sourceNamespace,
	)

	createResp, err := client.CreateBridge(ctx, &pb.CreateBridgeRequest{
		DeviceId:         deviceID,
		SourceDeployment: deploymentName,
		SourceNamespace:  sourceNamespace,
		Force:            force,
	})
	if err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	fmt.Fprintf(w, "\nBridge created successfully!\n")
	fmt.Fprintf(w, "  Namespace: %s\n", createResp.Namespace)
	fmt.Fprintf(w, "  Pod:       %s\n", createResp.PodName)
	fmt.Fprintf(w, "  Port:      %d\n", createResp.Port)

	// Step 6: Generate devcontainer config when a base config is provided.
	baseConfigFlag := c.String("devcontainer-config")
	if baseConfigFlag != "" || connectFlag {
		baseConfig, err := devcontainer.ResolveConfigPath(baseConfigFlag)
		if err != nil {
			return err
		}
		dcConfigPath, err := generateDevcontainerConfig(w, deploymentName, baseConfig, featureRef, c.Int("listen"), createResp)
		if err != nil {
			return err
		}
		if connectFlag {
			return startDevcontainer(ctx, dcConfigPath, r, w)
		}
	}

	return nil
}

// generateDevcontainerConfig creates a bridge devcontainer.json from a base config.
// It respects the KUBECONFIG env var by bind-mounting it into the container,
// unless the base config already sets containerEnv.KUBECONFIG.
// Returns the path to the generated config.
func generateDevcontainerConfig(w io.Writer, deploymentName, baseConfigPath, featureRef string, appPort int, resp *pb.CreateBridgeResponse) (string, error) {
	dcName := deploymentName
	if dcName == "" {
		dcName = "proxy"
	}

	// Place the generated config under the .devcontainer/ directory that contains
	// the base config. If the base config isn't already in a .devcontainer/ folder,
	// create one next to it.
	baseParent := filepath.Dir(baseConfigPath)
	var dcDir string
	if filepath.Base(baseParent) == ".devcontainer" {
		// Base is at <workspace>/.devcontainer/devcontainer.json — use the same .devcontainer/.
		dcDir = filepath.Join(baseParent, fmt.Sprintf("bridge-%s", dcName))
	} else {
		// Base is elsewhere — create a .devcontainer/ directory next to it.
		dcDir = filepath.Join(baseParent, ".devcontainer", fmt.Sprintf("bridge-%s", dcName))
	}
	dcConfigPath := filepath.Join(dcDir, "devcontainer.json")

	// Load from base config, then overlay bridge settings.
	cfg, err := devcontainer.Load(baseConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to load base devcontainer config: %w", err)
	}

	cfg.Name = "bridge-" + dcName
	bridgeServerAddr := fmt.Sprintf("k8spf:///%s.%s:%d", resp.PodName, resp.Namespace, resp.Port)
	cfg.SetFeature(featureRef, map[string]any{
		"bridgeVersion":    Version,
		"bridgeServerAddr": bridgeServerAddr,
		"forwardDomains":   "*",
		"appPort":          fmt.Sprintf("%d", appPort),
		"workspacePath":    "${containerWorkspaceFolder}",
	})
	cfg.EnsureCapAdd("NET_ADMIN")

	// Mount KUBECONFIG if set, unless the base config already configured it.
	if kubeconfigPath := os.Getenv("KUBECONFIG"); kubeconfigPath != "" {
		if _, exists := cfg.ContainerEnv["KUBECONFIG"]; !exists {
			absPath, err := filepath.Abs(kubeconfigPath)
			if err != nil {
				return "", fmt.Errorf("failed to resolve KUBECONFIG path: %w", err)
			}
			mountTarget := "/tmp/bridge-kubeconfig"
			cfg.SetMount(fmt.Sprintf("source=%s,target=%s,type=bind,readonly", absPath, mountTarget))
			cfg.EnsureContainerEnv("KUBECONFIG", mountTarget)
		}
	}

	if err := os.MkdirAll(dcDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create devcontainer directory: %w", err)
	}
	if err := cfg.Save(dcConfigPath); err != nil {
		return "", fmt.Errorf("failed to write devcontainer config: %w", err)
	}

	fmt.Fprintf(w, "\nDevcontainer config written to %s\n", dcConfigPath)
	return dcConfigPath, nil
}

// startDevcontainer starts the devcontainer and attaches an interactive shell.
func startDevcontainer(ctx context.Context, dcConfigPath string, r io.Reader, w io.Writer) error {
	// <workspace>/.devcontainer/bridge-<name>/devcontainer.json → <workspace>
	workspaceFolder := filepath.Dir(filepath.Dir(filepath.Dir(dcConfigPath)))
	dcClient := &devcontainer.Client{
		WorkspaceFolder: workspaceFolder,
		ConfigPath:      dcConfigPath,
		Stdin:           r,
		Stdout:          w,
		Stderr:          w,
	}

	slog.Info("Starting devcontainer", "config", dcConfigPath, "workspace", workspaceFolder)
	if err := dcClient.Up(ctx); err != nil {
		return fmt.Errorf("failed to start devcontainer: %w", err)
	}

	slog.Info("Devcontainer started, attaching shell")
	return dcClient.ExecAttached(ctx, []string{"bash"})
}
