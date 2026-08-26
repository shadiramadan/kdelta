// Package agent runs kdelta's model-driven flows: structuring fetched
// changelog text and assessing upgrade impact against a cluster.
//
// Runner is the execution boundary. Today it runs in-process against the
// Claude API (the model gathers extra context through restricted tools); the
// interface is the swap point for a sandboxed backend (see ROADMAP:
// Kubernetes agent sandbox) without touching the RPC layer.
package agent

import (
	"context"
	"time"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// ReleaseNotes is one version's fetched, verbatim release notes.
type ReleaseNotes struct {
	Version    string
	Body       string
	URL        string
	ReleasedAt time.Time
}

// ExtractRequest asks for normalized changes from fetched release notes.
type ExtractRequest struct {
	Package  *kdeltav1.PackageKey
	Releases []ReleaseNotes
	// Progress receives human-readable status; may be nil.
	Progress func(stage, message string)
	// Partial receives each version's changes as soon as its extraction
	// batch completes, for incremental rendering; may be nil.
	Partial func(*kdeltav1.VersionChanges)
}

// AssessRequest asks for an impact assessment of one stream upgrade.
type AssessRequest struct {
	Target      *kdeltav1.ResourceRef
	StreamID    string
	FromVersion string
	ToVersion   string
	ChangeSet   *kdeltav1.ChangeSet
	Inventory   *kdeltav1.ScanResponse
	// Cluster lets the agent inspect live cluster objects through a
	// restricted view; nil disables cluster tools.
	Cluster ClusterQuerier
	// Progress receives human-readable status; may be nil.
	Progress func(stage, message string)
}

// ClusterQuerier is the restricted cluster view handed to the agent as a
// tool backend. Implementations must never expose secret payloads.
type ClusterQuerier interface {
	// List returns trimmed objects for an allowlisted resource type.
	List(ctx context.Context, group, version, resource, namespace string) ([]map[string]any, error)
	// Allowed describes the listable resource types, for the tool prompt.
	Allowed() []string
}

// Runner executes model-driven flows.
type Runner interface {
	// ExtractChanges structures verbatim release notes into per-version
	// normalized changes.
	ExtractChanges(ctx context.Context, req ExtractRequest) ([]*kdeltav1.VersionChanges, error)
	// AssessImpact analyzes an upgrade against the cluster inventory,
	// gathering more context through the request's tools when needed.
	AssessImpact(ctx context.Context, req AssessRequest) (*kdeltav1.ImpactAssessment, error)
}
