package cmd

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/ref"
)

// Shell completions are served from the local cache (the last scan snapshot
// in SQLite) through the normal client path — fast, no cluster round-trip.
// Completion functions stay silent on error: an empty cache or unreachable
// server must never break the user's shell.

func completeResourceRefs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	_ = withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
		res, err := c.ListResources(ctx, connect.NewRequest(&kdeltav1.ListResourcesRequest{}))
		if err != nil {
			return err
		}
		for _, r := range res.Msg.GetResources() {
			canonical := ref.String(r.GetRef())
			if !strings.HasPrefix(canonical, toComplete) {
				continue
			}
			description := ""
			if stream := primaryStream(r); stream != nil {
				description = stream.GetId() + " " + stream.GetCurrent().GetValue()
			}
			completions = append(completions, cobra.CompletionWithDesc(canonical, description))
		}
		return nil
	})
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func completeStreamIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	target, err := ref.Parse(args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	_ = withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
		res, err := c.GetResource(ctx, connect.NewRequest(&kdeltav1.GetResourceRequest{Ref: target}))
		if err != nil {
			return err
		}
		for _, stream := range res.Msg.GetResource().GetStreams() {
			if !strings.HasPrefix(stream.GetId(), toComplete) {
				continue
			}
			completions = append(completions,
				cobra.CompletionWithDesc(stream.GetId(), stream.GetCurrent().GetValue()))
		}
		return nil
	})
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// registerRefCompletions wires resource-ref and stream completions onto a
// command taking a <resource-ref> argument and a --stream flag.
func registerRefCompletions(cmd *cobra.Command) {
	cmd.ValidArgsFunction = completeResourceRefs
	if cmd.Flags().Lookup("stream") != nil {
		_ = cmd.RegisterFlagCompletionFunc("stream", completeStreamIDs)
	}
}
