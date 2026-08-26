package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// addOutputFlag registers the shared machine-readable output selector.
func addOutputFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "", "output format: json (protobuf JSON of the response)")
}

func jsonOutput(cmd *cobra.Command) (bool, error) {
	value, err := cmd.Flags().GetString("output")
	if err != nil {
		return false, err
	}
	switch value {
	case "":
		return false, nil
	case "json":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported output format %q (supported: json)", value)
	}
}

// printProtoJSON renders a response message as multiline protobuf JSON on
// stdout.
func printProtoJSON(cmd *cobra.Command, message proto.Message) error {
	payload, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(message)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(payload))
	return nil
}
