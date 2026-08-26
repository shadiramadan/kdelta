package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/ref"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/vers"
)

// versionCacheTTL bounds how stale a cached upstream version list may be.
const versionCacheTTL = time.Hour

// ListVersions enumerates a stream's available versions from its source,
// scheme-ordered from the deployed version to the latest.
func (s *KdeltaService) ListVersions(
	ctx context.Context,
	req *connect.Request[kdeltav1.ListVersionsRequest],
) (*connect.Response[kdeltav1.ListVersionsResponse], error) {
	resource, stream, err := s.resourceStream(ctx, req.Msg.GetRef(), req.Msg.GetStreamId())
	if err != nil {
		return nil, err
	}
	all, latest, err := s.streamVersions(ctx, resource, stream)
	if err != nil {
		return nil, err
	}

	current := stream.GetCurrent().GetValue()
	scheme := stream.GetScheme()
	res := &kdeltav1.ListVersionsResponse{Current: current, Latest: latest}
	for _, v := range all {
		cmp, ok := vers.CompareOK(v.GetValue(), current, scheme)
		// Skip versions that cannot be ordered against the deployed one (e.g.
		// a non-semver tag in a semver stream): placing them in the list or
		// the behind-count would be a guess, not a fact.
		if !ok || cmp < 0 {
			continue
		}
		res.Versions = append(res.Versions, v)
		if cmp > 0 && !v.GetPrerelease() {
			res.VersionsBehind++
		}
	}
	return connect.NewResponse(res), nil
}

// streamVersions returns the stream's full ascending version list and the
// latest stable version, from cache when fresh.
func (s *KdeltaService) streamVersions(
	ctx context.Context,
	resource *kdeltav1.Resource,
	stream *kdeltav1.VersionStream,
) ([]*kdeltav1.Version, string, error) {
	pkg := resource.GetUpstream()
	if s.store != nil {
		cached, err := s.store.VersionList(ctx, pkg, stream.GetKind(), versionCacheTTL)
		switch {
		case err == nil:
			return cached.GetVersions(), cached.GetLatest(), nil
		case errors.Is(err, store.ErrNotFound):
			// Cache miss or stale entry: fall through and refetch.
		default:
			// A real store error must surface, not silently trigger a refetch
			// (which, against a remote store, would amplify load on every blip).
			return nil, "", fmt.Errorf("reading cached version list: %w", err)
		}
	}

	fetched, err := s.fetchVersions(ctx, resource, stream)
	if err != nil {
		return nil, "", err
	}
	sorted := vers.SortAscending(fetched, stream.GetScheme())
	latest := ""
	for _, v := range sorted {
		if !v.GetPrerelease() {
			latest = v.GetValue()
		}
	}
	if latest == "" && len(sorted) > 0 {
		latest = sorted[len(sorted)-1].GetValue()
	}

	if s.store != nil {
		_ = s.store.PutVersionList(ctx, pkg, stream.GetKind(),
			&kdeltav1.ListVersionsResponse{Versions: sorted, Latest: latest})
	}
	return sorted, latest, nil
}

// fetchVersions executes the stream's version source.
func (s *KdeltaService) fetchVersions(ctx context.Context, resource *kdeltav1.Resource, stream *kdeltav1.VersionStream) ([]*kdeltav1.Version, error) {
	if s.upstream == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("upstream fetching is not configured on this server"))
	}
	switch source := stream.GetSource().GetSource().(type) {
	case *kdeltav1.VersionSource_GithubReleases:
		releases, err := s.upstream.GitHubReleases(ctx, source.GithubReleases.GetOwner(), source.GithubReleases.GetRepo())
		if err != nil {
			return nil, err
		}
		versions := make([]*kdeltav1.Version, 0, len(releases))
		for _, release := range releases {
			versions = append(versions, &kdeltav1.Version{
				Value:      release.Tag,
				ReleasedAt: timestamppb.New(release.PublishedAt),
				Prerelease: release.Prerelease,
				Links: []*kdeltav1.Link{{
					Kind: kdeltav1.LinkKind_LINK_KIND_RELEASES,
					Url:  release.HTMLURL,
				}},
			})
		}
		return versions, nil
	case *kdeltav1.VersionSource_HelmRepository:
		charts, err := s.upstream.HelmChartVersions(ctx, source.HelmRepository.GetRepositoryUrl(), source.HelmRepository.GetChart())
		if err != nil {
			return nil, err
		}
		versions := make([]*kdeltav1.Version, 0, len(charts))
		for _, chart := range charts {
			versions = append(versions, &kdeltav1.Version{
				Value:      chart.Version,
				ReleasedAt: timestamppb.New(chart.Created),
				Deprecated: chart.Deprecated,
			})
		}
		return versions, nil
	case nil:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New(noSourceMessage(resource, stream)))
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("version source %T is not supported yet - see docs/ROADMAP.md", source))
	}
}

// noSourceMessage explains an unresolved stream and names the sibling
// streams that do have a version source, so the caller sees the remedy
// instead of guessing.
func noSourceMessage(resource *kdeltav1.Resource, stream *kdeltav1.VersionStream) string {
	var resolvable []string
	for _, sibling := range resource.GetStreams() {
		if sibling.GetSource().GetSource() != nil {
			resolvable = append(resolvable, sibling.GetId())
		}
	}
	msg := fmt.Sprintf("stream %q has no version source (upstream not resolved)", stream.GetId())
	if len(resolvable) == 0 {
		return msg + "; no stream on this resource can be resolved"
	}
	return fmt.Sprintf("%s; resolvable streams: %s", msg, strings.Join(resolvable, ", "))
}

// resourceStream looks up a resource and one of its streams (the primary
// stream when streamID is empty) from the cached scan.
func (s *KdeltaService) resourceStream(
	ctx context.Context,
	target *kdeltav1.ResourceRef,
	streamID string,
) (*kdeltav1.Resource, *kdeltav1.VersionStream, error) {
	scan, err := s.cachedScan(ctx)
	if err != nil {
		return nil, nil, err
	}
	var resource *kdeltav1.Resource
	for _, r := range scan.GetResources() {
		if refsEqual(r.GetRef(), target) {
			resource = r
			break
		}
	}
	if resource == nil {
		return nil, nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("resource %s not found in the cached scan - re-run a scan", ref.String(target)))
	}
	streams := resource.GetStreams()
	if len(streams) == 0 {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("resource %s has no version streams", ref.String(target)))
	}
	if streamID == "" {
		return resource, streams[0], nil
	}
	for _, stream := range streams {
		if stream.GetId() == streamID {
			return resource, stream, nil
		}
	}
	return nil, nil, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("stream %q not found on %s", streamID, ref.String(target)))
}

// scanForImpact returns the cached scan with its generation id (impact
// assessments are invalidated with the scan they came from).
func (s *KdeltaService) scanForImpact(ctx context.Context) (int64, *kdeltav1.ScanResponse, error) {
	if s.store == nil {
		return 0, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no cache is configured on this server"))
	}
	id, scan, err := s.store.LatestScan(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no scan cached yet - run a scan first"))
	}
	return id, scan, err
}
