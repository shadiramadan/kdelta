package agent

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// allowedResources is the agent's cluster-read allowlist. It mirrors the demo
// RBAC minus secrets: the agent must never see secret payloads, no matter
// what the underlying credentials permit.
var allowedResources = map[schema.GroupVersionResource]bool{
	{Group: "", Version: "v1", Resource: "namespaces"}:                                                  true,
	{Group: "", Version: "v1", Resource: "pods"}:                                                        true,
	{Group: "", Version: "v1", Resource: "services"}:                                                    true,
	{Group: "", Version: "v1", Resource: "serviceaccounts"}:                                             true,
	{Group: "apps", Version: "v1", Resource: "deployments"}:                                             true,
	{Group: "apps", Version: "v1", Resource: "statefulsets"}:                                            true,
	{Group: "apps", Version: "v1", Resource: "daemonsets"}:                                              true,
	{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}:                                  true,
	{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}:                                 true,
	{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"}:                          true,
	{Group: "cert-manager.io", Version: "v1", Resource: "issuers"}:                                      true,
	{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}:                               true,
	{Group: "acme.cert-manager.io", Version: "v1", Resource: "orders"}:                                  true,
	{Group: "acme.cert-manager.io", Version: "v1", Resource: "challenges"}:                              true,
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}: true,
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}:   true,
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}:                              true,
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}:                       true,
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}:                       true,
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}:                true,
	{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}:               true,
}

const maxListedObjects = 50

// ClusterView is the restricted ClusterQuerier backed by the cluster
// connection.
type ClusterView struct {
	client dynamic.Interface
}

// NewClusterView builds the restricted view from a cluster connection.
func NewClusterView(cfg *rest.Config) (*ClusterView, error) {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client: %w", err)
	}
	return &ClusterView{client: client}, nil
}

func (v *ClusterView) Allowed() []string {
	kinds := make([]string, 0, len(allowedResources))
	for gvr := range allowedResources {
		name := gvr.Resource
		if gvr.Group != "" {
			name = gvr.Resource + "." + gvr.Group
		}
		kinds = append(kinds, name)
	}
	sort.Strings(kinds)
	return kinds
}

func (v *ClusterView) List(ctx context.Context, group, version, resource, namespace string) ([]map[string]any, error) {
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if !allowedResources[gvr] {
		return nil, fmt.Errorf("resource %s is not in the agent's allowlist (allowed: %v)", gvr, v.Allowed())
	}
	list, err := v.client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: maxListedObjects})
	if err != nil {
		return nil, err
	}
	objects := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		objects = append(objects, trimObject(item.Object))
	}
	return objects, nil
}

// trimObject keeps what impact analysis needs (identity, spec, status
// conditions) and drops noise and anything that could smuggle sensitive data.
// The Secret resource is not listable at all (see allowedResources); this
// additionally redacts literal container env values, which operators sometimes
// inline as credentials on otherwise-listable objects (pods, workloads).
func trimObject(obj map[string]any) map[string]any {
	trimmed := map[string]any{}
	if meta, ok := obj["metadata"].(map[string]any); ok {
		trimmed["metadata"] = map[string]any{
			"name":      meta["name"],
			"namespace": meta["namespace"],
			"labels":    meta["labels"],
		}
	}
	for _, key := range []string{"spec", "status", "webhooks", "rules", "roleRef", "subjects"} {
		if value, ok := obj[key]; ok {
			trimmed[key] = value
		}
	}
	redactContainerEnvValues(trimmed)
	return trimmed
}

// redactContainerEnvValues walks a trimmed object for pod-template container
// lists and drops each env entry's literal "value" (keeping "name" and
// "valueFrom"), so a plaintext credential inlined as an env value never
// reaches the agent. Impact analysis needs env variable names, not values.
func redactContainerEnvValues(node any) {
	switch typed := node.(type) {
	case map[string]any:
		for _, listKey := range []string{"containers", "initContainers", "ephemeralContainers"} {
			containers, ok := typed[listKey].([]any)
			if !ok {
				continue
			}
			for _, c := range containers {
				container, ok := c.(map[string]any)
				if !ok {
					continue
				}
				envs, ok := container["env"].([]any)
				if !ok {
					continue
				}
				for _, e := range envs {
					if env, ok := e.(map[string]any); ok {
						delete(env, "value")
					}
				}
			}
		}
		for _, v := range typed {
			redactContainerEnvValues(v)
		}
	case []any:
		for _, v := range typed {
			redactContainerEnvValues(v)
		}
	}
}
