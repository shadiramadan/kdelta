package cmd

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/ref"
)

func newImpactCmd() *cobra.Command {
	impactCmd := &cobra.Command{
		Use:   "impact <resource-ref>",
		Short: "Assess the impact of upgrading a resource on the rest of the system",
		Long: `Analyze the changelog for a version range against everything else
detected in the cluster and report the likely blast radius of the upgrade,
resource by resource.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			rangeReq, err := changeRangeRequest(cmd, target)
			if err != nil {
				return err
			}
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				stream, err := c.AssessImpact(ctx, connect.NewRequest(&kdeltav1.AssessImpactRequest{
					Ref:         rangeReq.GetRef(),
					StreamId:    rangeReq.GetStreamId(),
					FromVersion: rangeReq.GetFromVersion(),
					ToVersion:   rangeReq.GetToVersion(),
				}))
				if err != nil {
					return err
				}
				defer stream.Close()
				for stream.Receive() {
					switch event := stream.Msg().GetEvent().(type) {
					case *kdeltav1.AssessImpactResponse_Progress:
						fmt.Fprintf(cmd.ErrOrStderr(), "» %s: %s\n", event.Progress.GetStage(), event.Progress.GetMessage())
					case *kdeltav1.AssessImpactResponse_Result:
						if asJSON {
							if err := printProtoJSON(cmd, event.Result); err != nil {
								return err
							}
							continue
						}
						renderImpact(cmd.OutOrStdout(), event.Result)
					}
				}
				return stream.Err()
			})
		},
	}
	addRangeFlags(impactCmd)
	addOutputFlag(impactCmd)
	registerRefCompletions(impactCmd)
	return impactCmd
}

func renderImpact(out io.Writer, a *kdeltav1.ImpactAssessment) {
	fmt.Fprintf(out, "%s %s: %s -> %s  [%s]\n\n%s\n",
		ref.String(a.GetTarget()), a.GetStreamId(),
		a.GetFromVersion(), a.GetToVersion(),
		severityLabel(a.GetOverallSeverity()), a.GetSummary())

	// The per-resource rollup leads: every touched resource with what the
	// upgrade means for it.
	if resources := a.GetResources(); len(resources) > 0 {
		fmt.Fprintf(out, "\nAffected resources:\n")
		for _, r := range resources {
			fmt.Fprintf(out, "  [%s] %s\n      %s\n",
				severityLabel(r.GetSeverity()), ref.String(r.GetRef()), r.GetExplanation())
			for _, obj := range r.GetObjects() {
				fmt.Fprintf(out, "      - %s/%s %s/%s\n",
					obj.GetApiVersion(), obj.GetKind(), obj.GetNamespace(), obj.GetName())
			}
		}
	}

	for _, finding := range a.GetFindings() {
		fmt.Fprintf(out, "\n[%s] %s (%s)\n  %s\n",
			severityLabel(finding.GetSeverity()), finding.GetTitle(),
			ref.String(finding.GetAffected()), finding.GetRationale())
		for _, action := range finding.GetActions() {
			timing := "after upgrade"
			if action.GetBeforeUpgrade() {
				timing = "before upgrade"
			}
			fmt.Fprintf(out, "  -> (%s) %s\n", timing, action.GetDescription())
		}
	}

	if gaps := a.GetGaps(); len(gaps) > 0 {
		fmt.Fprintf(out, "\nUnknowns:\n")
		for _, gap := range gaps {
			fmt.Fprintf(out, "  ? %s\n", gap.GetDescription())
		}
	}
}
