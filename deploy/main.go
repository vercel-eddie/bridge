// deploy seeds a local k3d cluster with the bridge administrator and test server.
//
// Usage:
//
//	go run deploy/main.go [-cluster name]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/vercel-eddie/bridge/pkg/k8s/kube"
	"github.com/vercel-eddie/bridge/pkg/k8s/manifests"
)

const (
	adminImage      = "administrator:local"
	testServerImage = "test-api-server:local"
)

func main() {
	cluster := flag.String("cluster", "k3s-default", "k3d cluster name")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	root, err := findProjectRoot()
	if err != nil {
		log.Fatalf("Failed to find project root: %v", err)
	}

	// 1. Build images.
	log.Println("Building administrator image...")
	dockerBuild(root, filepath.Join("services", "administrator", "Dockerfile"), adminImage)

	log.Println("Building test server image...")
	dockerBuild(filepath.Join(root, "e2e", "testserver"), "Dockerfile", testServerImage)

	// 2. Import images into k3d.
	log.Printf("Importing images into k3d cluster %q...\n", *cluster)
	run("k3d", "image", "import", adminImage, testServerImage, "-c", *cluster)

	// 3. Connect to the cluster and apply manifests.
	restCfg, err := kube.RestConfig(kube.Config{})
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	log.Println("Applying administrator manifests...")
	if err := manifests.Apply(ctx, restCfg, filepath.Join(root, "deploy", "k8s", "administrator.yaml"), map[string]string{
		"{{ADMINISTRATOR_IMAGE}}": adminImage,
		"{{PROXY_IMAGE}}":         adminImage,
	}); err != nil {
		log.Fatalf("Failed to apply administrator manifests: %v", err)
	}

	log.Println("Applying test server manifests...")
	if err := manifests.Apply(ctx, restCfg, filepath.Join(root, "deploy", "k8s", "testserver.yaml"), map[string]string{
		"{{TESTSERVER_IMAGE}}": testServerImage,
	}); err != nil {
		log.Fatalf("Failed to apply test server manifests: %v", err)
	}

	// 4. Wait for rollouts.
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	log.Println("Waiting for administrator deployment...")
	if err := waitForDeployment(ctx, clientset, "bridge", "administrator"); err != nil {
		log.Fatalf("Administrator not ready: %v", err)
	}

	log.Println("Waiting for test server deployment...")
	if err := waitForDeployment(ctx, clientset, "test-workloads", "test-api-server"); err != nil {
		log.Fatalf("Test server not ready: %v", err)
	}

	log.Println("Done. Cluster is seeded.")
}

func dockerBuild(contextDir, dockerfile, tag string) {
	run("docker", "build", "-t", tag, "-f", filepath.Join(contextDir, dockerfile), contextDir)
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("%s %v failed: %v", name, args, err)
	}
}

func waitForDeployment(ctx context.Context, clientset kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		deploy, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return deploy.Status.ReadyReplicas > 0, nil
	})
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (go.mod)")
		}
		dir = parent
	}
}
