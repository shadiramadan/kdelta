package cmd

import (
	"github.com/spf13/cobra"

	"github.com/shadiramadan/kdelta/internal/client"
	"github.com/shadiramadan/kdelta/internal/server"
	"github.com/shadiramadan/kdelta/internal/store"
)

func newServeCmd() *cobra.Command {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the kdelta API and web UI",
		Long: `Run the long-lived kdelta server: the ConnectRPC API under /rpc/ and
the embedded web UI everywhere else.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bind, err := cmd.Flags().GetString("bind")
			if err != nil {
				return err
			}
			path := flagString(cmd, "db")
			if path == "" {
				if path, err = store.DefaultPath(); err != nil {
					return err
				}
			}
			st, err := store.Open(path)
			if err != nil {
				return err
			}
			defer st.Close()

			cmd.Printf("kdelta serving on %s (cache: %s)\n", bind, path)
			return server.ListenAndServe(cmd.Context(), bind, server.NewKdeltaService(client.StandardDeps(st, path)))
		},
	}
	serveCmd.Flags().String("bind", ":8080", "address to serve the API and UI on")
	return serveCmd
}
