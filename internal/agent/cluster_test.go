package agent

import "testing"

// TestTrimObjectRedactsContainerEnvValues verifies that literal container env
// values (a common place operators inline credentials) are stripped from
// objects handed to the agent, while env names and valueFrom refs are kept.
func TestTrimObjectRedactsContainerEnvValues(t *testing.T) {
	pod := map[string]any{
		"metadata": map[string]any{"name": "app", "namespace": "demo", "extra": "drop me"},
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name": "app",
					"env": []any{
						map[string]any{"name": "API_TOKEN", "value": "s3cr3t-literal"},
						map[string]any{"name": "REF", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "s"}}},
					},
				},
			},
			"initContainers": []any{
				map[string]any{
					"name": "init",
					"env":  []any{map[string]any{"name": "INIT_PW", "value": "also-secret"}},
				},
			},
		},
	}

	trimmed := trimObject(pod)

	spec := trimmed["spec"].(map[string]any)
	containers := spec["containers"].([]any)
	env := containers[0].(map[string]any)["env"].([]any)
	first := env[0].(map[string]any)
	if _, present := first["value"]; present {
		t.Errorf("literal env value not redacted: %v", first)
	}
	if first["name"] != "API_TOKEN" {
		t.Errorf("env name should be kept, got %v", first["name"])
	}
	if _, ok := env[1].(map[string]any)["valueFrom"]; !ok {
		t.Errorf("valueFrom ref should be kept: %v", env[1])
	}

	initEnv := spec["initContainers"].([]any)[0].(map[string]any)["env"].([]any)
	if _, present := initEnv[0].(map[string]any)["value"]; present {
		t.Errorf("init container env value not redacted: %v", initEnv[0])
	}

	// Unrelated metadata is still trimmed to the identity subset.
	if _, present := trimmed["metadata"].(map[string]any)["extra"]; present {
		t.Errorf("metadata not trimmed to identity fields: %v", trimmed["metadata"])
	}
}
