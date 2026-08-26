// Package cmd defines the kdelta CLI. Commands are thin: they parse
// arguments, call the ConnectRPC service through the shared client
// (in-process by default, remote via --server), and render responses.
//
// The command tree is built fresh by newRootCmd() rather than assembled from
// package-level globals and init() side effects, so each invocation (and each
// test) gets an isolated tree with its own flag state.
package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/client"
)

// Root returns a fresh root command with every subcommand attached, for
// execution by main.
func Root() *cobra.Command { return newRootCmd() }

// newRootCmd assembles the command tree. Persistent flags are registered here
// and read back through cmd.Flags() where needed (see clientConfig) rather
// than bound to package globals.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "kdelta",
		Short: "Detect deployed Kubernetes resources and analyze their version deltas",
		Long: `kdelta answers four questions about a Kubernetes cluster: what's
deployed, what version is it, what changed upstream since then, and what
happens to the rest of the system if you upgrade it.`,
		SilenceUsage: true,
	}
	root.PersistentFlags().String("server", "",
		"URL of a remote kdelta server (default: spawn the server in-process)")
	root.PersistentFlags().String("db", "",
		"path of the local cache database (default: the per-user cache dir)")

	root.AddCommand(
		newEchoCmd(),
		newScanCmd(),
		newResourcesCmd(),
		newGetCmd(),
		newVersionsCmd(),
		newChangesCmd(),
		newImpactCmd(),
		newServeCmd(),
		newAgentToolsCmd(),
	)
	return root
}

// clientConfig reads the persistent --server/--db flags off any command in the
// tree (they are inherited from the root).
func clientConfig(cmd *cobra.Command) client.Config {
	return client.Config{
		ServerURL: flagString(cmd, "server"),
		DBPath:    flagString(cmd, "db"),
	}
}

// flagString reads a string flag, returning "" if it is absent or unset.
func flagString(cmd *cobra.Command, name string) string {
	v, err := cmd.Flags().GetString(name)
	if err != nil {
		return ""
	}
	return v
}

// withClient dials the API per the persistent flags and runs fn against it.
func withClient(cmd *cobra.Command, fn func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error) error {
	c, cleanup, err := client.New(cmd.Context(), clientConfig(cmd))
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(cmd.Context(), c)
}
