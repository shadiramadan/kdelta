package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
)

func newTestClient(t *testing.T) kdeltav1connect.KdeltaServiceClient {
	t.Helper()
	srv := httptest.NewServer(Handler(NewKdeltaService(Deps{})))
	t.Cleanup(srv.Close)
	return kdeltav1connect.NewKdeltaServiceClient(srv.Client(), srv.URL+RPCPrefix)
}

func TestEcho(t *testing.T) {
	client := newTestClient(t)

	res, err := client.Echo(context.Background(),
		connect.NewRequest(&kdeltav1.EchoRequest{Message: "hello, cluster"}))
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if got, want := res.Msg.GetMessage(), "hello, cluster"; got != want {
		t.Errorf("Echo message = %q, want %q", got, want)
	}
}

func TestEchoValidation(t *testing.T) {
	client := newTestClient(t)

	tests := []struct {
		name    string
		message string
	}{
		{name: "empty message", message: ""},
		{name: "oversized message", message: string(make([]byte, 5000))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Echo(context.Background(),
				connect.NewRequest(&kdeltav1.EchoRequest{Message: tt.message}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("Echo(%q) error code = %v, want %v", tt.message, connect.CodeOf(err), connect.CodeInvalidArgument)
			}
		})
	}
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(Handler(NewKdeltaService(Deps{})))
	t.Cleanup(srv.Close)

	check := func(t *testing.T, service string, wantStatus int, wantBody string) {
		t.Helper()
		res, err := srv.Client().Post(srv.URL+"/grpc.health.v1.Health/Check",
			"application/json", strings.NewReader(fmt.Sprintf(`{"service":%q}`, service)))
		if err != nil {
			t.Fatalf("health check: %v", err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if res.StatusCode != wantStatus {
			t.Errorf("status = %d, want %d (body %s)", res.StatusCode, wantStatus, body)
		}
		if !strings.Contains(string(body), wantBody) {
			t.Errorf("body = %s, want it to contain %q", body, wantBody)
		}
	}

	t.Run("whole process", func(t *testing.T) {
		check(t, "", http.StatusOK, "SERVING")
	})
	t.Run("kdelta service", func(t *testing.T) {
		check(t, "kdelta.v1.KdeltaService", http.StatusOK, "SERVING")
	})
	t.Run("unknown service", func(t *testing.T) {
		check(t, "not.a.Service", http.StatusNotFound, "not_found")
	})
}

func TestUIRouteIsWired(t *testing.T) {
	// The embed dir's contents depend on whether `task ui:embed` ran before
	// compiling, so accept both branches: the real UI (200) or the placeholder
	// hint (501). Anything else means the mux fallthrough to the UI broke.
	srv := httptest.NewServer(Handler(NewKdeltaService(Deps{})))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotImplemented {
		t.Errorf("GET / status = %d, want %d (embedded UI) or %d (placeholder)",
			res.StatusCode, http.StatusOK, http.StatusNotImplemented)
	}
}

func TestUIRouteSetsSecurityHeaders(t *testing.T) {
	srv := httptest.NewServer(Handler(NewKdeltaService(Deps{})))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer res.Body.Close()

	wantHeaders := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	}
	for header, want := range wantHeaders {
		if got := res.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q; got %q", directive, csp)
		}
	}
}

func TestRPCRouteHasNoUISecurityHeaders(t *testing.T) {
	// The CSP/frame headers must not ride on RPC responses — gRPC/Connect
	// clients are not browsers and the UI hardening is irrelevant there.
	srv := httptest.NewServer(Handler(NewKdeltaService(Deps{})))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Post(
		srv.URL+RPCPrefix+"/kdelta.v1.KdeltaService/Echo",
		"application/json", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("POST Echo: %v", err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("RPC response carries a CSP header %q; UI headers must be UI-only", got)
	}
}
