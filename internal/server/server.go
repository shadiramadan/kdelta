// Package server implements the kdelta ConnectRPC API and the HTTP server
// that exposes it alongside the embedded web UI.
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/validate"

	"k8s.io/client-go/rest"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/ui"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// RPCPrefix is the path prefix the ConnectRPC API is mounted under; client
// base URLs must include it. Everything outside the prefix serves the web UI.
const RPCPrefix = "/rpc"

const shutdownGrace = 5 * time.Second

// maxRequestBytes bounds a single decoded request message. Generous enough for
// a legitimate caller-supplied change set (bounded further by protovalidate),
// small enough to reject abusive payloads early.
const maxRequestBytes = 16 << 20 // 16 MiB

// Deps are the service's injectable dependencies. Nil members disable the
// RPCs that need them with a FailedPrecondition error rather than a crash.
type Deps struct {
	// Store caches scan snapshots, version lists, change sets, and impact
	// assessments between calls (see internal/store for invalidation).
	Store *store.Store
	// Detectors is the registry scan runs.
	Detectors *detect.Registry
	// RESTConfig resolves the target cluster connection, lazily per scan.
	RESTConfig func() (*rest.Config, error)
	// Upstream fetches version lists and release notes.
	Upstream *upstream.Client
	// Agent runs the model-driven flows (extraction, impact assessment).
	Agent agent.Runner
}

// KdeltaService implements kdelta.v1.KdeltaService.
type KdeltaService struct {
	store      *store.Store
	detectors  *detect.Registry
	restConfig func() (*rest.Config, error)
	upstream   *upstream.Client
	agent      agent.Runner
}

// NewKdeltaService wires the service's dependencies.
func NewKdeltaService(deps Deps) *KdeltaService {
	return &KdeltaService{
		store:      deps.Store,
		detectors:  deps.Detectors,
		restConfig: deps.RESTConfig,
		upstream:   deps.Upstream,
		agent:      deps.Agent,
	}
}

var _ kdeltav1connect.KdeltaServiceHandler = (*KdeltaService)(nil)

// Echo returns the message it was sent. It is the placeholder RPC proving the
// proto -> server -> client pipeline; see docs/ROADMAP.md for the real surface.
func (s *KdeltaService) Echo(
	_ context.Context,
	req *connect.Request[kdeltav1.EchoRequest],
) (*connect.Response[kdeltav1.EchoResponse], error) {
	return connect.NewResponse(&kdeltav1.EchoResponse{Message: req.Msg.GetMessage()}), nil
}

// Handler returns the complete HTTP handler: the ConnectRPC API under
// RPCPrefix (with protovalidate enforcement), the embedded UI everywhere else.
func Handler(svc *KdeltaService) http.Handler {
	api := http.NewServeMux()
	api.Handle(kdeltav1connect.NewKdeltaServiceHandler(
		svc,
		connect.WithInterceptors(validate.NewInterceptor()),
		// Bound request bodies: protovalidate caps individual fields, this caps
		// the whole message so an oversized payload is rejected before it is
		// decoded or reaches the model prompt.
		connect.WithReadMaxBytes(maxRequestBytes),
	))

	mux := http.NewServeMux()
	mux.Handle(RPCPrefix+"/", http.StripPrefix(RPCPrefix, api))
	// Health and reflection live at the root, NOT under RPCPrefix: kubelet's
	// native gRPC probes and raw gRPC tooling cannot traverse path prefixes.
	mux.Handle(grpchealth.NewHandler(
		grpchealth.NewStaticChecker(kdeltav1connect.KdeltaServiceName),
	))
	reflector := grpcreflect.NewStaticReflector(
		kdeltav1connect.KdeltaServiceName,
		grpchealth.HealthV1ServiceName,
	)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))
	// Security headers apply only to the served UI, not the RPC/health/
	// reflection routes above (gRPC clients must not receive them).
	mux.Handle("/", uiSecurityHeaders(ui.Handler()))
	return mux
}

// uiSecurityHeaders hardens the served web UI with a same-origin CSP, denied
// framing, and MIME-sniffing off. script/style allow 'unsafe-inline' because
// the Next static export ships inline hydration scripts and Tailwind emits
// inline styles, and a static export cannot use per-request nonces; the
// javascript:-URL vector the CSP would otherwise catch is already closed at
// the source (helm detector isSafeHTTPURL + UI safeHref). connect-src 'self'
// still bounds where script can exfiltrate to, and frame-ancestors/object-src
// close clickjacking and plugin vectors.
func uiSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Serve serves the kdelta API and UI on l until ctx is canceled. It speaks
// HTTP/1.1 and h2c so gRPC clients work without TLS.
func Serve(ctx context.Context, l net.Listener, svc *KdeltaService) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Handler:           Handler(svc),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		// A context-triggered stop is a normal shutdown, not an error.
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ListenAndServe binds addr (TCP) and calls Serve.
func ListenAndServe(ctx context.Context, addr string, svc *KdeltaService) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return Serve(ctx, l, svc)
}
