package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/shadiramadan/kdelta/internal/store"
)

// MCPToolsConfig configures the restricted tool surface served to an
// agent-harness subprocess over MCP stdio.
type MCPToolsConfig struct {
	// Store supplies the scan inventory.
	Store *store.Store
	// Cluster is the restricted live view; nil disables the cluster tool.
	Cluster ClusterQuerier
}

type listObjectsInput struct {
	Group     string `json:"group" jsonschema:"API group; empty string for the core group"`
	Version   string `json:"version" jsonschema:"API version, e.g. v1"`
	Resource  string `json:"resource" jsonschema:"plural resource name, e.g. certificates"`
	Namespace string `json:"namespace" jsonschema:"namespace to list in; empty for all namespaces"`
}

// ServeMCPTools serves kdelta's agent tools on stdio until the client
// disconnects. It backs the hidden `kdelta agent-tools` command that the
// Claude CLI harness spawns via --mcp-config.
func ServeMCPTools(ctx context.Context, cfg MCPToolsConfig) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:  "kdelta",
		Title: "kdelta agent tools",
	}, nil)

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "list_inventory",
			Description: "List kdelta's detected resource inventory for this cluster (refs, version streams, conditions).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if cfg.Store == nil {
				return nil, nil, errors.New("no cache configured")
			}
			_, scan, err := cfg.Store.LatestScan(ctx)
			if errors.Is(err, store.ErrNotFound) {
				return textToolResult("no scan cached yet - the inventory is empty"), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			payload, err := protojson.Marshal(inventorySummary(scan))
			if err != nil {
				return nil, nil, err
			}
			return textToolResult(string(payload)), nil, nil
		})

	if cfg.Cluster != nil {
		mcp.AddTool(server,
			&mcp.Tool{
				Name: "list_cluster_objects",
				Description: fmt.Sprintf(
					"List live Kubernetes objects (trimmed to metadata/spec/status; the Secret resource is not listable and container env values are redacted). Allowed resources: %v.",
					cfg.Cluster.Allowed()),
			},
			func(ctx context.Context, _ *mcp.CallToolRequest, in listObjectsInput) (*mcp.CallToolResult, any, error) {
				objects, err := cfg.Cluster.List(ctx, in.Group, in.Version, in.Resource, in.Namespace)
				if err != nil {
					// Return errors as content so the model can adjust course.
					return textToolResult("error: " + err.Error()), nil, nil
				}
				payload, err := json.Marshal(objects)
				if err != nil {
					return nil, nil, err
				}
				return textToolResult(string(payload)), nil, nil
			})
	}

	return server.Run(ctx, &mcp.StdioTransport{})
}

func textToolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
