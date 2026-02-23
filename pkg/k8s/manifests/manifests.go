// Package manifests provides utilities for applying multi-document Kubernetes
// YAML manifests via the dynamic client-go API.
package manifests

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// Apply reads a multi-document YAML file, performs placeholder substitutions,
// and applies each resource via the dynamic k8s API. Creates are idempotent —
// existing resources are silently skipped.
func Apply(ctx context.Context, cfg *rest.Config, path string, substitutions map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}

	content := string(raw)
	for placeholder, value := range substitutions {
		content = strings.ReplaceAll(content, placeholder, value)
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create discovery client: %w", err)
	}

	groupResources, err := restmapper.GetAPIGroupResources(disco)
	if err != nil {
		return fmt.Errorf("discover API groups: %w", err)
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	docs := bytes.Split([]byte(content), []byte("\n---"))
	for i, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		if err := applyDocument(ctx, dc, mapper, doc); err != nil {
			return fmt.Errorf("document %d in %s: %w", i, path, err)
		}
	}

	return nil
}

var yamlDecoder = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

func applyDocument(ctx context.Context, dc dynamic.Interface, mapper meta.RESTMapper, doc []byte) error {
	obj := &unstructured.Unstructured{}
	_, gvk, err := yamlDecoder.Decode(doc, nil, obj)
	if err != nil {
		return fmt.Errorf("decode document: %w", err)
	}

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("map %s to resource: %w", gvk, err)
	}

	var resource dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		resource = dc.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		resource = dc.Resource(mapping.Resource)
	}

	if _, err := resource.Create(ctx, obj, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create %s %s: %w", gvk.Kind, obj.GetName(), err)
	}

	return nil
}
