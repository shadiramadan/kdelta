package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/ref"
)

func newGetCmd() *cobra.Command {
	getCmd := &cobra.Command{
		Use:   "get <resource-ref>",
		Short: "Show a detected resource's streams, links, and state",
		Long: `Show one resource from the cached scan: its upstream identity, version
streams (with their sources), conditions, and backing cluster objects.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			asJSON, err := jsonOutput(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				res, err := c.GetResource(ctx, connect.NewRequest(&kdeltav1.GetResourceRequest{Ref: target}))
				if err != nil {
					return err
				}
				if asJSON {
					return printProtoJSON(cmd, res.Msg)
				}
				renderResource(cmd.OutOrStdout(), res.Msg)
				return nil
			})
		},
	}
	addOutputFlag(getCmd)
	registerRefCompletions(getCmd)
	return getCmd
}

func renderResource(out io.Writer, res *kdeltav1.GetResourceResponse) {
	resource := res.GetResource()
	fmt.Fprintf(out, "%s\n", ref.String(resource.GetRef()))

	if upstream := resource.GetUpstream(); upstream != nil {
		registry := upstream.GetRegistryUrl()
		if registry == "" {
			registry = "(registry unresolved)"
		}
		fmt.Fprintf(out, "upstream: %s/%s %s\n", upstream.GetSystem(), upstream.GetName(), registry)
	}
	for _, link := range resource.GetLinks() {
		fmt.Fprintf(out, "link: %s %s\n", strings.ToLower(strings.TrimPrefix(link.GetKind().String(), "LINK_KIND_")), link.GetUrl())
	}

	fmt.Fprintf(out, "streams:\n")
	for _, stream := range resource.GetStreams() {
		source := "no version source"
		switch s := stream.GetSource().GetSource().(type) {
		case *kdeltav1.VersionSource_GithubReleases:
			source = "github " + s.GithubReleases.GetOwner() + "/" + s.GithubReleases.GetRepo()
		case *kdeltav1.VersionSource_HelmRepository:
			source = "helm repo " + s.HelmRepository.GetRepositoryUrl()
		}
		constraint := ""
		if stream.GetConstraint() != "" {
			constraint = " (tracks " + stream.GetConstraint() + ")"
		}
		fmt.Fprintf(out, "  %s\t%s%s\t%s\n", stream.GetId(), stream.GetCurrent().GetValue(), constraint, source)
	}

	for _, condition := range resource.GetConditions() {
		fmt.Fprintf(out, "condition: %s=%s %s\n", condition.GetType(),
			strings.ToLower(strings.TrimPrefix(condition.GetStatus().String(), "CONDITION_STATUS_")),
			condition.GetReason())
	}
	for _, object := range resource.GetObjects() {
		namespaced := object.GetName()
		if object.GetNamespace() != "" {
			namespaced = object.GetNamespace() + "/" + object.GetName()
		}
		fmt.Fprintf(out, "object: %s %s %s\n", object.GetApiVersion(), object.GetKind(), namespaced)
	}
	for _, edge := range res.GetEdges() {
		fmt.Fprintf(out, "edge: %s %s %s\n", ref.String(edge.GetParent()),
			strings.ToLower(strings.TrimPrefix(edge.GetKind().String(), "EDGE_KIND_")), ref.String(edge.GetChild()))
	}
}
