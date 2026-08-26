package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/ref"
	"github.com/shadiramadan/kdelta/internal/store"
)

// Scan runs the registered detectors against the target cluster, caches the
// snapshot (superseding the previous scan and everything derived from it),
// and returns it.
func (s *KdeltaService) Scan(
	ctx context.Context,
	req *connect.Request[kdeltav1.ScanRequest],
) (*connect.Response[kdeltav1.ScanResponse], error) {
	if s.detectors == nil || s.restConfig == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("scanning is not configured on this server"))
	}
	cfg, err := s.restConfig()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	result, err := s.detectors.Run(ctx, detect.Request{
		Config:     cfg,
		Namespaces: req.Msg.GetNamespaces(),
		Selector:   req.Msg.GetSelector(),
	}, req.Msg.GetDetectors())
	if err != nil {
		return nil, err
	}
	s.resolveUpstreams(ctx, result.Resources)

	scan := &kdeltav1.ScanResponse{
		Resources: result.Resources,
		Edges:     result.Edges,
		ScannedAt: timestamppb.Now(),
	}
	if s.store != nil {
		if _, err := s.store.SaveScan(ctx, scan); err != nil {
			return nil, fmt.Errorf("caching scan: %w", err)
		}
	}
	return connect.NewResponse(scan), nil
}

// ListResources pages through the cached scan's resources.
func (s *KdeltaService) ListResources(
	ctx context.Context,
	req *connect.Request[kdeltav1.ListResourcesRequest],
) (*connect.Response[kdeltav1.ListResourcesResponse], error) {
	scan, err := s.cachedScan(ctx)
	if err != nil {
		return nil, err
	}
	resources := scan.GetResources()

	offset := 0
	if token := req.Msg.GetPageToken(); token != "" {
		if offset, err = strconv.Atoi(token); err != nil || offset < 0 || offset > len(resources) {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid page token %q", token))
		}
	}
	end := len(resources)
	if size := int(req.Msg.GetPageSize()); size > 0 && offset+size < end {
		end = offset + size
	}
	res := &kdeltav1.ListResourcesResponse{Resources: resources[offset:end]}
	if end < len(resources) {
		res.NextPageToken = strconv.Itoa(end)
	}
	return connect.NewResponse(res), nil
}

// GetResource returns one cached resource with the hierarchy edges that
// touch it.
func (s *KdeltaService) GetResource(
	ctx context.Context,
	req *connect.Request[kdeltav1.GetResourceRequest],
) (*connect.Response[kdeltav1.GetResourceResponse], error) {
	scan, err := s.cachedScan(ctx)
	if err != nil {
		return nil, err
	}
	target := req.Msg.GetRef()
	res := &kdeltav1.GetResourceResponse{}
	for _, r := range scan.GetResources() {
		if refsEqual(r.GetRef(), target) {
			res.Resource = r
			break
		}
	}
	if res.Resource == nil {
		// Name the ref and the remedy, matching resourceStream's message: a
		// bare "not found" leaves the caller guessing whether they mistyped
		// the ref or simply have a stale scan.
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("resource %s not found in the cached scan - re-run a scan", ref.String(target)))
	}
	for _, edge := range scan.GetEdges() {
		if refsEqual(edge.GetParent(), target) || refsEqual(edge.GetChild(), target) {
			res.Edges = append(res.Edges, edge)
		}
	}
	return connect.NewResponse(res), nil
}

func (s *KdeltaService) cachedScan(ctx context.Context) (*kdeltav1.ScanResponse, error) {
	if s.store == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no cache is configured on this server"))
	}
	_, scan, err := s.store.LatestScan(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no scan cached yet - run a scan first"))
	}
	if err != nil {
		return nil, err
	}
	return scan, nil
}

func refsEqual(a, b *kdeltav1.ResourceRef) bool {
	return a.GetDetector() == b.GetDetector() &&
		a.GetNamespace() == b.GetNamespace() &&
		a.GetName() == b.GetName()
}
