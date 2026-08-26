package server

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/store"
)

// AssessImpact analyzes a proposed stream upgrade against the cluster
// inventory: the change set is resolved (cache or sources), then the agent
// cross-references it against live cluster state through restricted tools.
func (s *KdeltaService) AssessImpact(
	ctx context.Context,
	req *connect.Request[kdeltav1.AssessImpactRequest],
	stream *connect.ServerStream[kdeltav1.AssessImpactResponse],
) error {
	if s.agent == nil {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("impact assessment requires the agent runner; this server was built without one"))
	}
	progress := func(stage, message string) {
		_ = stream.Send(&kdeltav1.AssessImpactResponse{
			Event: &kdeltav1.AssessImpactResponse_Progress{
				Progress: &kdeltav1.Progress{Stage: stage, Message: message},
			},
		})
	}
	send := func(assessment *kdeltav1.ImpactAssessment) error {
		return stream.Send(&kdeltav1.AssessImpactResponse{
			Event: &kdeltav1.AssessImpactResponse_Result{Result: assessment},
		})
	}

	target := req.Msg.GetRef()
	streamID := req.Msg.GetStreamId()
	if streamID == "" {
		if _, s, err := s.resourceStream(ctx, target, ""); err == nil {
			streamID = s.GetId()
		}
	}

	// The change set: client-provided, cached, or resolved from sources.
	set := req.Msg.GetChangeSet()
	var err error
	if set == nil {
		set, err = s.resolveChangeSet(ctx, target, streamID,
			req.Msg.GetFromVersion(), req.Msg.GetToVersion(), progress, nil)
		if err != nil {
			return err
		}
	}

	scanID, inventory, err := s.scanForImpact(ctx)
	if err != nil {
		return err
	}
	cached, err := s.store.Impact(ctx, target, streamID, set.GetFromVersion(), set.GetToVersion())
	switch {
	case err == nil:
		progress("cache", "serving the cached assessment (re-scan to invalidate)")
		return send(cached)
	case errors.Is(err, store.ErrNotFound):
		// Not assessed yet: fall through and run the assessment.
	default:
		// Fail closed rather than silently re-running a paid model assessment
		// because a store read hiccupped.
		return fmt.Errorf("reading cached impact assessment: %w", err)
	}

	// Best-effort restricted cluster view; without it the agent works from
	// the inventory alone and reports the reduced confidence as gaps.
	var cluster agent.ClusterQuerier
	if s.restConfig != nil {
		if cfg, err := s.restConfig(); err == nil {
			if view, err := agent.NewClusterView(cfg); err == nil {
				cluster = view
			}
		}
	}
	if cluster == nil {
		progress("assessing", "no cluster connection - assessing from the inventory alone")
	}

	assessment, err := s.agent.AssessImpact(ctx, agent.AssessRequest{
		Target:      target,
		StreamID:    streamID,
		FromVersion: set.GetFromVersion(),
		ToVersion:   set.GetToVersion(),
		ChangeSet:   set,
		Inventory:   inventory,
		Cluster:     cluster,
		Progress:    progress,
	})
	if errors.Is(err, agent.ErrNoCredentials) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if err != nil {
		return err
	}
	// Identity fields are the cache key; the server owns them regardless of
	// what the runner echoed back.
	assessment.Target = target
	assessment.StreamId = streamID
	assessment.FromVersion = set.GetFromVersion()
	assessment.ToVersion = set.GetToVersion()

	if err := s.store.PutImpact(ctx, scanID, assessment); err != nil {
		return err
	}
	return send(assessment)
}
