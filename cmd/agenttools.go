package cmd

import (
	"github.com/spf13/cobra"

	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/kube"
	"github.com/shadiramadan/kdelta/internal/store"
)

// agentToolsCmd serves kdelta's restricted agent tool surface over MCP stdio.
// It is spawned by the Claude CLI agent harness (via --mcp-config), not run
// by people — hence hidden.
func newAgentToolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "agent-tools",
		Hidden: true,
		Short:  "Serve the agent tool surface over MCP stdio",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := flagString(cmd, "db")
			if path == "" {
				var err error
				if path, err = store.DefaultPath(); err != nil {
					return err
				}
			}
			st, err := store.Open(path)
			if err != nil {
				return err
			}
			defer st.Close()

			cfg := agent.MCPToolsConfig{Store: st}
			// Best-effort cluster view: without a cluster connection the
			// inventory tool still serves.
			if restCfg, err := kube.RESTConfig(); err == nil {
				if view, err := agent.NewClusterView(restCfg); err == nil {
					cfg.Cluster = view
				}
			}
			return agent.ServeMCPTools(cmd.Context(), cfg)
		},
	}
}
