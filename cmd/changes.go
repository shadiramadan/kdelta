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

func newChangesCmd() *cobra.Command {
	changesCmd := &cobra.Command{
		Use:     "changes <resource-ref>",
		Aliases: []string{"changelog"},
		Short:   "Show the changelog for a resource across a version range",
		Long: `Fetch the changelog for a detected resource between two versions.
Release notes are fetched from the stream's change sources and structured by
AI extraction, falling back to the verbatim notes when no model credential is
available.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			req, err := changeRangeRequest(cmd, target)
			if err != nil {
				return err
			}
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				stream, err := c.GetChanges(ctx, connect.NewRequest(req))
				if err != nil {
					return err
				}
				defer stream.Close()
				out := cmd.OutOrStdout()
				streamed := false
				for stream.Receive() {
					switch event := stream.Msg().GetEvent().(type) {
					case *kdeltav1.GetChangesResponse_Progress:
						fmt.Fprintf(cmd.ErrOrStderr(), "» %s: %s\n", event.Progress.GetStage(), event.Progress.GetMessage())
					case *kdeltav1.GetChangesResponse_Partial:
						if asJSON {
							continue // JSON mode emits only the final ChangeSet
						}
						streamed = true
						renderVersionChanges(out, event.Partial)
					case *kdeltav1.GetChangesResponse_Result:
						if asJSON {
							if err := printProtoJSON(cmd, event.Result); err != nil {
								return err
							}
							continue
						}
						if streamed {
							renderChangeSetFooter(out, event.Result)
							continue
						}
						renderChangeSet(out, event.Result)
					}
				}
				return stream.Err()
			})
		},
	}
	addRangeFlags(changesCmd)
	addOutputFlag(changesCmd)
	registerRefCompletions(changesCmd)
	return changesCmd
}

// changeRangeRequest reads the shared --stream/--from/--to flags into a
// GetChangesRequest.
func changeRangeRequest(cmd *cobra.Command, target *kdeltav1.ResourceRef) (*kdeltav1.GetChangesRequest, error) {
	streamID, err := cmd.Flags().GetString("stream")
	if err != nil {
		return nil, err
	}
	from, err := cmd.Flags().GetString("from")
	if err != nil {
		return nil, err
	}
	to, err := cmd.Flags().GetString("to")
	if err != nil {
		return nil, err
	}
	return &kdeltav1.GetChangesRequest{
		Ref:         target,
		StreamId:    streamID,
		FromVersion: from,
		ToVersion:   to,
	}, nil
}

func renderChangeSet(out io.Writer, cs *kdeltav1.ChangeSet) {
	fmt.Fprintf(out, "%s %s -> %s\n", cs.GetPackage().GetName(), cs.GetFromVersion(), cs.GetToVersion())
	for _, vc := range cs.GetVersions() {
		renderVersionChanges(out, vc)
	}
	renderChangeSetFooter(out, cs)
}

// renderVersionChanges prints one version's block; partial events use it to
// render the changelog incrementally as extraction batches complete.
func renderVersionChanges(out io.Writer, vc *kdeltav1.VersionChanges) {
	fmt.Fprintf(out, "\n%s\n", vc.GetVersion())
	for _, change := range vc.GetChanges() {
		fmt.Fprintf(out, "  [%s] %s\n", changeTypeLabel(change.GetType()), change.GetSummary())
		for _, path := range change.GetAffectedPaths() {
			fmt.Fprintf(out, "        affects %s\n", path)
		}
	}
}

func renderChangeSetFooter(out io.Writer, cs *kdeltav1.ChangeSet) {
	for _, guide := range cs.GetMigrationGuides() {
		fmt.Fprintf(out, "\nmigration guide: %s\n", guide.GetLink().GetUrl())
	}
}

func addRangeFlags(cmd *cobra.Command) {
	cmd.Flags().String("stream", "", "version stream (default: the primary stream)")
	cmd.Flags().String("from", "", "starting version (default: the deployed version)")
	cmd.Flags().String("to", "", "ending version (default: latest)")
}
