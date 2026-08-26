package cmd

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/ref"
)

func newResourcesCmd() *cobra.Command {
	resourcesCmd := &cobra.Command{
		Use:     "resources",
		Aliases: []string{"ls"},
		Short:   "List detected resources from the cached scan",
		Long: `List the resources detected by the most recent scan, from the local
cache — no cluster round-trip. Run "kdelta scan" to refresh.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				res, err := c.ListResources(ctx, connect.NewRequest(&kdeltav1.ListResourcesRequest{}))
				if err != nil {
					return err
				}
				if asJSON {
					return printProtoJSON(cmd, res.Msg)
				}
				out := cmd.OutOrStdout()
				for _, r := range res.Msg.GetResources() {
					current := "?"
					if stream := primaryStream(r); stream != nil {
						current = stream.GetId() + "@" + stream.GetCurrent().GetValue()
					}
					fmt.Fprintf(out, "%s\t%s\n", ref.String(r.GetRef()), current)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%d resources (cached; run `kdelta scan` to refresh)\n",
					len(res.Msg.GetResources()))
				return nil
			})
		},
	}
	addOutputFlag(resourcesCmd)
	return resourcesCmd
}
