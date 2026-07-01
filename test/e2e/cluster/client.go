// Package cluster connects to a Kubernetes cluster. It never creates or
// destroys clusters — that is handled by test/e2e/hack/create-cluster.py.
package cluster

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/config"
)

// Client bundles the pieces of the cluster connection tests need: a typed
// clientset for CRUD and a rest.Config for building SPDY port-forwards. It is
// the common handle threaded through the nodes/plugin/pods/portforward
// packages.
type Client struct {
	Config *config.Config
	REST   *rest.Config
	Typed  kubernetes.Interface
}

// NewClient connects to the cluster pointed at by cfg.Kubeconfig.
func NewClient(cfg config.Config) (*Client, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", cfg.Kubeconfig, err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}

	return &Client{Config: &cfg, REST: restCfg, Typed: clientset}, nil
}
