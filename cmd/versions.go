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

func newVersionsCmd() *cobra.Command {
	versionsCmd := &cobra.Command{
		Use:   "versions <resource-ref>",
		Short: "List versions of a resource since the deployed one",
		Long: `List all published versions of a detected resource between the
deployed version and the latest, resolved deterministically from upstream
sources (releases, chart repositories, registries) without AI.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			streamID, err := cmd.Flags().GetString("stream")
			if err != nil {
				return err
			}
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				res, err := c.ListVersions(ctx, connect.NewRequest(&kdeltav1.ListVersionsRequest{
					Ref:      target,
					StreamId: streamID,
				}))
				if err != nil {
					return err
				}
				if asJSON {
					return printProtoJSON(cmd, res.Msg)
				}
				out := cmd.OutOrStdout()
				for _, v := range res.Msg.GetVersions() {
					marker := " "
					switch v.GetValue() {
					case res.Msg.GetCurrent():
						marker = "*" // deployed
					case res.Msg.GetLatest():
						marker = ">" // latest
					}
					fmt.Fprintf(out, "%s %s\n", marker, v.GetValue())
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "current %s, latest %s (%d behind)\n",
					res.Msg.GetCurrent(), res.Msg.GetLatest(), res.Msg.GetVersionsBehind())
				return nil
			})
		},
	}
	versionsCmd.Flags().String("stream", "", "version stream to list (default: the primary stream)")
	addOutputFlag(versionsCmd)
	registerRefCompletions(versionsCmd)
	return versionsCmd
}
