package client

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{DBPath: filepath.Join(t.TempDir(), "kdelta.db")}
}

func TestInProcessRoundTrip(t *testing.T) {
	c, cleanup, err := New(context.Background(), testConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	res, err := c.Echo(context.Background(),
		connect.NewRequest(&kdeltav1.EchoRequest{Message: "over the socket"}))
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if got, want := res.Msg.GetMessage(), "over the socket"; got != want {
		t.Errorf("Echo message = %q, want %q", got, want)
	}
}

func TestCleanupIsIdempotentlySafe(t *testing.T) {
	c, cleanup, err := New(context.Background(), testConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := c.Echo(context.Background(),
		connect.NewRequest(&kdeltav1.EchoRequest{Message: "before cleanup"}))
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if res.Msg.GetMessage() != "before cleanup" {
		t.Errorf("Echo message = %q, want %q", res.Msg.GetMessage(), "before cleanup")
	}

	cleanup()

	// After cleanup the socket is gone: calls must fail, not hang.
	if _, err := c.Echo(context.Background(),
		connect.NewRequest(&kdeltav1.EchoRequest{Message: "after cleanup"})); err == nil {
		t.Error("Echo after cleanup succeeded, want transport error")
	}
}
