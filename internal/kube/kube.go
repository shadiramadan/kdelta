// Package kube resolves how kdelta reaches the target cluster: the
// in-cluster service account when deployed, otherwise the active kubeconfig
// context.
package kube

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTConfig resolves the cluster connection. Deferred kubeconfig loading
// falls back to the in-cluster config automatically when no kubeconfig is
// present, which is exactly the dev-vs-deployed split kdelta wants.
func RESTConfig() (*rest.Config, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("resolving cluster config (kubeconfig or in-cluster): %w", err)
	}
	return cfg, nil
}
