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

func newScanCmd() *cobra.Command {
	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Detect deployed resources, their versions, and upstream links",
		Long: `Scan the cluster (in-cluster or current kubeconfig context) with the
registered detectors and list each detected resource, its current and latest
version, and a link to its repository and changelog.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			namespaces, err := cmd.Flags().GetStringArray("namespace")
			if err != nil {
				return err
			}
			selector, err := cmd.Flags().GetString("selector")
			if err != nil {
				return err
			}
			detectors, err := cmd.Flags().GetStringArray("detector")
			if err != nil {
				return err
			}
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				res, err := c.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{
					Namespaces: namespaces,
					Selector:   selector,
					Detectors:  detectors,
				}))
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
						current = stream.GetCurrent().GetValue()
					}
					fmt.Fprintf(out, "%s\t%s\n", ref.String(r.GetRef()), current)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%d resources detected\n", len(res.Msg.GetResources()))
				return nil
			})
		},
	}
	scanCmd.Flags().StringArrayP("namespace", "n", nil, "namespace to scan (repeatable; default: all)")
	scanCmd.Flags().StringP("selector", "l", "", "label selector filtering candidate objects")
	scanCmd.Flags().StringArray("detector", nil, "detector to run (repeatable; default: all)")
	addOutputFlag(scanCmd)
	return scanCmd
}
