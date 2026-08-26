package cmd

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
)

func newEchoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "echo <message>",
		Short: "Round-trip a message through the kdelta API",
		Long: `Send a message through the full RPC pipeline and print the response.

This is the placeholder command proving the client/server plumbing: without
--server it spawns the server in-process over a unix socket, exactly as the
real commands do.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, c kdeltav1connect.KdeltaServiceClient) error {
				res, err := c.Echo(ctx, connect.NewRequest(&kdeltav1.EchoRequest{Message: args[0]}))
				if err != nil {
					return err
				}
				// cobra's cmd.Println goes to stderr; the result belongs on
				// stdout so it is pipeable.
				fmt.Fprintln(cmd.OutOrStdout(), res.Msg.GetMessage())
				return nil
			})
		},
	}
}
