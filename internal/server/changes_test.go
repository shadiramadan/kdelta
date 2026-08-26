package server_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/server"
	"github.com/shadiramadan/kdelta/internal/servertest"
)

// TestGetChangesStreamsPartialsBeforeResult verifies the incremental contract:
// a live extraction emits per-version Partial events as batches complete, and
// the terminal Result carries the complete set — so clients can render the
// changelog progressively.
func TestGetChangesStreamsPartialsBeforeResult(t *testing.T) {
	backend := servertest.New(t)
	client := kdeltav1connect.NewKdeltaServiceClient(http.DefaultClient, backend.URL+server.RPCPrefix)

	stream, err := client.GetChanges(context.Background(), connect.NewRequest(&kdeltav1.GetChangesRequest{
		Ref:      servertest.CertManagerResource().GetRef(),
		StreamId: "app",
	}))
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var partials []string
	var result *kdeltav1.ChangeSet
	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *kdeltav1.GetChangesResponse_Partial:
			if result != nil {
				t.Errorf("partial %q arrived after the final result", event.Partial.GetVersion())
			}
			partials = append(partials, event.Partial.GetVersion())
		case *kdeltav1.GetChangesResponse_Result:
			result = event.Result
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	// The fake forge serves v1.18.0 and v1.21.1 in the (v1.17.2, v1.21.1] range.
	if len(partials) != 2 {
		t.Fatalf("partial events = %v, want one per version (v1.18.0, v1.21.1)", partials)
	}
	if result == nil {
		t.Fatal("no terminal result event")
	}
	if len(result.GetVersions()) != len(partials) {
		t.Errorf("final set has %d versions but %d partials streamed", len(result.GetVersions()), len(partials))
	}
}
