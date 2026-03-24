package kube

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes client-go interfaces for use throughout nstack.
type Client struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	restConfig    *rest.Config
}

// NewClient creates a Kubernetes client from the given kubeconfig path.
// If kubeconfig is empty, it falls back to in-cluster configuration.
func NewClient(kubeconfig string) (*Client, error) {
	var cfg *rest.Config
	var err error

	if kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		clientset:     cs,
		dynamicClient: dc,
		restConfig:    cfg,
	}, nil
}

// NewClientFromInterfaces creates a Client from pre-built interfaces.
// This is primarily useful for testing with fake clients.
func NewClientFromInterfaces(cs kubernetes.Interface, dc dynamic.Interface, rc *rest.Config) *Client {
	return &Client{
		clientset:     cs,
		dynamicClient: dc,
		restConfig:    rc,
	}
}

// Clientset returns the typed Kubernetes clientset.
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// DynamicClient returns the unstructured dynamic client.
func (c *Client) DynamicClient() dynamic.Interface {
	return c.dynamicClient
}

// RESTConfig returns the underlying REST configuration.
func (c *Client) RESTConfig() *rest.Config {
	return c.restConfig
}
