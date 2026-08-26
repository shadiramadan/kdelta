package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/client-go/rest"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/store"
)

type fakeDetector struct {
	result detect.Result
}

func (f fakeDetector) Name() string { return "helm" }

func (f fakeDetector) Detect(context.Context, detect.Request) (detect.Result, error) {
	return f.result, nil
}

func fakeResource(name string) *kdeltav1.Resource {
	return &kdeltav1.Resource{
		Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: name},
		DisplayName: name,
		Streams: []*kdeltav1.VersionStream{{
			Id:      "chart",
			Kind:    kdeltav1.VersionKind_VERSION_KIND_CHART,
			Current: &kdeltav1.DetectedVersion{Value: "v1.0.0"},
		}},
	}
}

func newScanTestClient(t *testing.T) kdeltav1connect.KdeltaServiceClient {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kdelta.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	svc := NewKdeltaService(Deps{
		Store: st,
		Detectors: detect.NewRegistry(fakeDetector{result: detect.Result{
			Resources: []*kdeltav1.Resource{fakeResource("cert-manager"), fakeResource("other")},
		}}),
		RESTConfig: func() (*rest.Config, error) { return &rest.Config{}, nil },
	})
	srv := httptest.NewServer(Handler(svc))
	t.Cleanup(srv.Close)
	return kdeltav1connect.NewKdeltaServiceClient(srv.Client(), srv.URL+RPCPrefix)
}

func TestScanListGetRoundTrip(t *testing.T) {
	client := newScanTestClient(t)
	ctx := context.Background()

	t.Run("ListResources before any scan", func(t *testing.T) {
		_, err := client.ListResources(ctx, connect.NewRequest(&kdeltav1.ListResourcesRequest{}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("error code = %v, want %v", connect.CodeOf(err), connect.CodeFailedPrecondition)
		}
	})

	scan, err := client.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{}))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scan.Msg.GetResources()) != 2 || scan.Msg.GetScannedAt() == nil {
		t.Fatalf("Scan = %d resources (scanned_at %v), want 2 with a timestamp",
			len(scan.Msg.GetResources()), scan.Msg.GetScannedAt())
	}

	t.Run("ListResources pages through the cached scan", func(t *testing.T) {
		first, err := client.ListResources(ctx, connect.NewRequest(&kdeltav1.ListResourcesRequest{PageSize: 1}))
		if err != nil {
			t.Fatalf("ListResources: %v", err)
		}
		if len(first.Msg.GetResources()) != 1 || first.Msg.GetNextPageToken() == "" {
			t.Fatalf("first page = %d resources, token %q; want 1 with a token",
				len(first.Msg.GetResources()), first.Msg.GetNextPageToken())
		}
		second, err := client.ListResources(ctx, connect.NewRequest(&kdeltav1.ListResourcesRequest{
			PageSize:  1,
			PageToken: first.Msg.GetNextPageToken(),
		}))
		if err != nil {
			t.Fatalf("ListResources page 2: %v", err)
		}
		if len(second.Msg.GetResources()) != 1 || second.Msg.GetNextPageToken() != "" {
			t.Errorf("second page = %d resources, token %q; want 1 and no token",
				len(second.Msg.GetResources()), second.Msg.GetNextPageToken())
		}
	})

	t.Run("GetResource", func(t *testing.T) {
		res, err := client.GetResource(ctx, connect.NewRequest(&kdeltav1.GetResourceRequest{
			Ref: &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "cert-manager"},
		}))
		if err != nil {
			t.Fatalf("GetResource: %v", err)
		}
		if res.Msg.GetResource().GetDisplayName() != "cert-manager" {
			t.Errorf("resource = %v, want cert-manager", res.Msg.GetResource())
		}
		_, err = client.GetResource(ctx, connect.NewRequest(&kdeltav1.GetResourceRequest{
			Ref: &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "absent"},
		}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("GetResource(absent) code = %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
		}
	})
}

func TestScanUnconfiguredReturnsFailedPrecondition(t *testing.T) {
	client := newTestClient(t) // Deps{}: no store, detectors, or cluster config
	_, err := client.Scan(context.Background(), connect.NewRequest(&kdeltav1.ScanRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("Scan error code = %v, want %v", connect.CodeOf(err), connect.CodeFailedPrecondition)
	}
}
