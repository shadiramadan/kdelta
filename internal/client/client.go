// Package client constructs the ConnectRPC client every CLI command talks
// through. Local and remote runs share one code path: with no server URL the
// server is spawned in-process behind a unix domain socket, so pointing at a
// remote server (--server) changes nothing but the transport.
package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/detect"
	helmdetect "github.com/shadiramadan/kdelta/internal/detect/helm"
	"github.com/shadiramadan/kdelta/internal/kube"
	"github.com/shadiramadan/kdelta/internal/server"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// Detectors builds the standard detector registry every in-process server
// (CLI and `kdelta serve`) runs.
func Detectors() *detect.Registry {
	return detect.NewRegistry(helmdetect.New())
}

// StandardDeps assembles the full dependency set shared by `kdelta serve`
// and the in-process server: detectors, upstream fetchers (GITHUB_TOKEN
// raises rate limits when set), and the agent runner.
func StandardDeps(st *store.Store, dbPath string) server.Deps {
	return server.Deps{
		Store:      st,
		Detectors:  Detectors(),
		RESTConfig: kube.RESTConfig,
		Upstream:   &upstream.Client{GitHubToken: os.Getenv("GITHUB_TOKEN")},
		Agent:      agentRunner(dbPath),
	}
}

// agentRunner picks the model-execution backend: the Claude Agent SDK
// harness (the `claude` CLI, which can bill the operator's Claude
// subscription) when available, else the Claude API SDK. KDELTA_AGENT=claude
// or =api forces the choice.
func agentRunner(dbPath string) agent.Runner {
	switch os.Getenv("KDELTA_AGENT") {
	case "api":
		return agent.NewAnthropic()
	case "claude":
		return agent.NewClaudeCode(dbPath)
	}
	if _, err := exec.LookPath("claude"); err == nil {
		return agent.NewClaudeCode(dbPath)
	}
	return agent.NewAnthropic()
}

// Config selects where the client connects and what local state it uses.
type Config struct {
	// ServerURL of a remote kdelta server. Empty spawns the server
	// in-process over a unix socket.
	ServerURL string
	// DBPath of the local cache database for in-process mode. Empty uses
	// store.DefaultPath(). Ignored when ServerURL is set (the remote server
	// owns its own cache).
	DBPath string
}

// New returns a client for the kdelta API and a cleanup function.
func New(ctx context.Context, cfg Config) (kdeltav1connect.KdeltaServiceClient, func(), error) {
	if cfg.ServerURL != "" {
		client := kdeltav1connect.NewKdeltaServiceClient(http.DefaultClient, cfg.ServerURL+server.RPCPrefix)
		return client, func() {}, nil
	}
	return newInProcess(ctx, cfg)
}

func newInProcess(ctx context.Context, cfg Config) (kdeltav1connect.KdeltaServiceClient, func(), error) {
	dbPath := cfg.DBPath
	if dbPath == "" {
		var err error
		if dbPath, err = store.DefaultPath(); err != nil {
			return nil, nil, err
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening local cache: %w", err)
	}

	dir, err := os.MkdirTemp("", "kdelta-*")
	if err != nil {
		_ = st.Close()
		return nil, nil, fmt.Errorf("creating socket directory: %w", err)
	}
	socket := filepath.Join(dir, "kdelta.sock")

	l, err := net.Listen("unix", socket)
	if err != nil {
		_ = st.Close()
		_ = os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("listening on %s: %w", socket, err)
	}

	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The listener is open before any RPC is issued, so requests can't
		// race the server; Serve errors surface as RPC transport errors.
		_ = server.Serve(serveCtx, l, server.NewKdeltaService(StandardDeps(st, dbPath)))
	}()

	cleanup := func() {
		stop()
		<-done
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	// The URL host is a placeholder: DialContext above always dials the socket.
	client := kdeltav1connect.NewKdeltaServiceClient(
		&http.Client{Transport: transport},
		"http://kdelta"+server.RPCPrefix,
	)
	return client, cleanup, nil
}
