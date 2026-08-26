package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/vers"
)

// GetChanges resolves the change information for a stream's version range,
// streaming progress while sources are fetched and structured.
func (s *KdeltaService) GetChanges(
	ctx context.Context,
	req *connect.Request[kdeltav1.GetChangesRequest],
	stream *connect.ServerStream[kdeltav1.GetChangesResponse],
) error {
	progress := func(stage, message string) {
		_ = stream.Send(&kdeltav1.GetChangesResponse{
			Event: &kdeltav1.GetChangesResponse_Progress{
				Progress: &kdeltav1.Progress{Stage: stage, Message: message},
			},
		})
	}
	partial := func(vc *kdeltav1.VersionChanges) {
		_ = stream.Send(&kdeltav1.GetChangesResponse{
			Event: &kdeltav1.GetChangesResponse_Partial{Partial: vc},
		})
	}
	set, err := s.resolveChangeSet(ctx,
		req.Msg.GetRef(), req.Msg.GetStreamId(),
		req.Msg.GetFromVersion(), req.Msg.GetToVersion(), progress, partial)
	if err != nil {
		return err
	}
	return stream.Send(&kdeltav1.GetChangesResponse{
		Event: &kdeltav1.GetChangesResponse_Result{Result: set},
	})
}

// resolveChangeSet returns the change set for a stream range, from cache when
// present, otherwise by fetching the stream's change sources and structuring
// them (AI extraction with a verbatim fallback when no credentials are
// available).
func (s *KdeltaService) resolveChangeSet(
	ctx context.Context,
	target *kdeltav1.ResourceRef,
	streamID, fromVersion, toVersion string,
	progress func(stage, message string),
	partial func(*kdeltav1.VersionChanges),
) (*kdeltav1.ChangeSet, error) {
	resource, stream, err := s.resourceStream(ctx, target, streamID)
	if err != nil {
		return nil, err
	}
	if fromVersion == "" {
		fromVersion = stream.GetCurrent().GetValue()
	}
	if toVersion == "" {
		// Surface the real blocker (no version source, unsupported source,
		// upstream failure) instead of collapsing it into a generic
		// version-range complaint.
		_, latest, err := s.streamVersions(ctx, resource, stream)
		if err != nil {
			return nil, err
		}
		toVersion = latest
	}
	if fromVersion == "" || toVersion == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("could not determine the version range; pass from/to explicitly"))
	}

	pkg := resource.GetUpstream()
	if s.store != nil {
		cached, err := s.store.ChangeSet(ctx, pkg, stream.GetKind(), fromVersion, toVersion)
		switch {
		case err == nil:
			progress("cache", "serving the cached change set")
			return cached, nil
		case errors.Is(err, store.ErrNotFound):
			// Not cached yet: fall through and resolve it.
		default:
			return nil, fmt.Errorf("reading cached change set: %w", err)
		}
	}

	releases, err := s.fetchReleaseNotes(ctx, stream, fromVersion, toVersion, progress)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("no releases found between %s and %s", fromVersion, toVersion))
	}

	versions, err := s.structureReleases(ctx, pkg, releases, progress, partial)
	if err != nil {
		return nil, err
	}

	set := &kdeltav1.ChangeSet{
		Package:     pkg,
		Kind:        stream.GetKind(),
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Versions:    versions,
	}
	if s.store != nil {
		if err := s.store.PutChangeSet(ctx, set); err != nil {
			return nil, fmt.Errorf("caching change set: %w", err)
		}
	}
	return set, nil
}

// fetchReleaseNotes executes the stream's first supported change source for
// the requested range, ascending.
func (s *KdeltaService) fetchReleaseNotes(
	ctx context.Context,
	stream *kdeltav1.VersionStream,
	fromVersion, toVersion string,
	progress func(stage, message string),
) ([]agent.ReleaseNotes, error) {
	if s.upstream == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("upstream fetching is not configured on this server"))
	}
	for _, source := range stream.GetChanges() {
		notes, ok := source.GetSource().(*kdeltav1.ChangeSource_ReleaseNotes)
		if !ok {
			continue
		}
		owner, repo, err := githubOwnerRepo(notes.ReleaseNotes.GetRepositoryUrl())
		if err != nil {
			return nil, err
		}
		progress("fetching", fmt.Sprintf("reading release notes from %s/%s", owner, repo))
		releases, err := s.upstream.GitHubReleases(ctx, owner, repo)
		if err != nil {
			return nil, err
		}
		scheme := stream.GetScheme()
		var selected []agent.ReleaseNotes
		for _, release := range releases {
			// An upgrade changelog covers stable releases in (from, to];
			// prereleases duplicate their final release's content.
			fromCmp, fromOK := vers.CompareOK(release.Tag, fromVersion, scheme)
			toCmp, toOK := vers.CompareOK(release.Tag, toVersion, scheme)
			// A tag that cannot be ordered against the range bounds cannot be
			// placed in the range; skip it rather than silently include or
			// drop it based on an ambiguous 0.
			if release.Prerelease || !fromOK || !toOK || fromCmp <= 0 || toCmp > 0 {
				continue
			}
			selected = append(selected, agent.ReleaseNotes{
				Version:    release.Tag,
				Body:       release.Body,
				URL:        release.HTMLURL,
				ReleasedAt: release.PublishedAt,
			})
		}
		// GitHub orders by publication, which interleaves maintenance lines;
		// the change set wants version order, oldest first.
		sort.SliceStable(selected, func(i, j int) bool {
			return vers.Compare(selected[i].Version, selected[j].Version, scheme) < 0
		})
		return selected, nil
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition,
		errors.New("this stream has no supported change source - see docs/ROADMAP.md for planned sources"))
}

// structureReleases turns fetched notes into normalized per-version changes:
// AI extraction when credentials allow, verbatim otherwise.
func (s *KdeltaService) structureReleases(
	ctx context.Context,
	pkg *kdeltav1.PackageKey,
	releases []agent.ReleaseNotes,
	progress func(stage, message string),
	partial func(*kdeltav1.VersionChanges),
) ([]*kdeltav1.VersionChanges, error) {
	if s.agent != nil {
		versions, err := s.agent.ExtractChanges(ctx, agent.ExtractRequest{
			Package:  pkg,
			Releases: releases,
			Progress: progress,
			Partial:  partial,
		})
		if err == nil {
			return versions, nil
		}
		if !errors.Is(err, agent.ErrNoCredentials) {
			return nil, err
		}
		progress("extracting", "no Claude API credentials - returning verbatim release notes")
	}
	versions := make([]*kdeltav1.VersionChanges, 0, len(releases))
	for _, release := range releases {
		vc := &kdeltav1.VersionChanges{
			Version:    release.Version,
			ReleasedAt: timestamppb.New(release.ReleasedAt),
			Changes: []*kdeltav1.Change{{
				Type:       kdeltav1.ChangeType_CHANGE_TYPE_OTHER,
				Summary:    "Release notes (unstructured)",
				Detail:     release.Body,
				Confidence: 1.0,
			}},
			Provenance: &kdeltav1.Provenance{
				Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_FETCHED,
				SourceUrl:   release.URL,
				CollectedAt: timestamppb.New(time.Now()),
			},
		}
		versions = append(versions, vc)
		if partial != nil {
			partial(vc)
		}
	}
	return versions, nil
}

func githubOwnerRepo(repositoryURL string) (owner, repo string, err error) {
	u, err := url.Parse(repositoryURL)
	if err != nil || u.Host != "github.com" {
		return "", "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("release-notes source %q is not a github.com repository", repositoryURL))
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("release-notes source %q lacks owner/repo", repositoryURL))
	}
	return parts[0], parts[1], nil
}
