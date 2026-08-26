package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/ref"
)

// API-backend tuning. The shared prompt/batch constants live in prompts.go.
const (
	defaultModel        = "claude-opus-5"
	assessMaxIterations = 16
	responseMaxTokens   = 16000
)

// Anthropic runs flows against the Claude API. Credentials resolve from the
// environment (ANTHROPIC_API_KEY or an `ant auth login` profile).
type Anthropic struct {
	client anthropic.Client
	model  string
	effort string
}

// NewAnthropic builds the in-process runner. The model comes from
// ANTHROPIC_MODEL when set.
func NewAnthropic() *Anthropic {
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultModel
	}
	return &Anthropic{client: anthropic.NewClient(), model: model, effort: effortFromEnv()}
}

func progress(fn func(stage, message string), stage, message string) {
	if fn != nil {
		fn(stage, message)
	}
}

// ErrNoCredentials marks Claude API authentication failures so callers can
// degrade gracefully (verbatim change sets) or fail with a clear remedy.
var ErrNoCredentials = errors.New("claude API credentials missing or invalid - set ANTHROPIC_API_KEY in the server environment")

// wrapAPIError makes credential problems actionable instead of cryptic.
func wrapAPIError(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) && (apierr.StatusCode == 401 || apierr.StatusCode == 403) {
		return fmt.Errorf("%w (%v)", ErrNoCredentials, err)
	}
	// The SDK fails client-side (not an *anthropic.Error) when no credential
	// source resolves at all.
	if strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		return fmt.Errorf("%w (%v)", ErrNoCredentials, err)
	}
	return fmt.Errorf("calling Claude API: %w", err)
}

func (a *Anthropic) ExtractChanges(ctx context.Context, req ExtractRequest) ([]*kdeltav1.VersionChanges, error) {
	urlByVersion := map[string]string{}
	for _, release := range req.Releases {
		urlByVersion[release.Version] = release.URL
	}

	var out []*kdeltav1.VersionChanges
	for start := 0; start < len(req.Releases); start += extractionBatchSize {
		end := min(start+extractionBatchSize, len(req.Releases))
		batch := req.Releases[start:end]
		progress(req.Progress, "extracting",
			fmt.Sprintf("structuring release notes %s..%s (%d/%d)",
				batch[0].Version, batch[len(batch)-1].Version, end, len(req.Releases)))

		prompt, err := buildExtractPrompt(req.Package, batch)
		if err != nil {
			return nil, err
		}
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(a.model),
			MaxTokens: responseMaxTokens,
			System:    []anthropic.TextBlockParam{{Text: extractSystemPrompt}},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
			},
		}
		if a.effort != "" {
			params.OutputConfig = anthropic.OutputConfigParam{
				Effort: anthropic.OutputConfigEffort(a.effort),
			}
		}
		resp, err := a.client.Messages.New(ctx, params)
		if err != nil {
			return nil, wrapAPIError(err)
		}
		text := messageText(resp)
		var set kdeltav1.ChangeSet
		if err := unmarshalModelJSON(text, &set); err != nil {
			return nil, fmt.Errorf("parsing extraction output: %w", err)
		}
		for _, vc := range set.GetVersions() {
			vc.Provenance = &kdeltav1.Provenance{
				Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_EXTRACTED,
				Model:       a.model,
				SourceUrl:   urlByVersion[vc.GetVersion()],
				CollectedAt: timestamppb.New(time.Now()),
			}
			out = append(out, vc)
			if req.Partial != nil {
				req.Partial(vc)
			}
		}
	}
	return out, nil
}

type listClusterObjectsInput struct {
	Group     string `json:"group" jsonschema:"description=API group; empty string for the core group"`
	Version   string `json:"version" jsonschema:"required,description=API version, e.g. v1"`
	Resource  string `json:"resource" jsonschema:"required,description=plural resource name, e.g. certificates"`
	Namespace string `json:"namespace" jsonschema:"description=namespace to list in; empty for all namespaces"`
}

type listInventoryInput struct{}

func (a *Anthropic) AssessImpact(ctx context.Context, req AssessRequest) (*kdeltav1.ImpactAssessment, error) {
	inventoryPayload, err := protojson.Marshal(inventorySummary(req.Inventory))
	if err != nil {
		return nil, err
	}

	inventoryTool, err := toolrunner.NewBetaToolFromJSONSchema(
		"list_inventory",
		"List kdelta's detected resource inventory for this cluster (refs, version streams, conditions).",
		func(_ context.Context, _ listInventoryInput) (anthropic.BetaToolResultBlockParamContentUnion, error) {
			progress(req.Progress, "agent", "reading the scan inventory")
			return textResult(string(inventoryPayload)), nil
		},
	)
	if err != nil {
		return nil, err
	}

	tools := []anthropic.BetaTool{inventoryTool}
	if req.Cluster != nil {
		clusterTool, err := toolrunner.NewBetaToolFromJSONSchema(
			"list_cluster_objects",
			fmt.Sprintf("List live Kubernetes objects (trimmed to metadata/spec/status; the Secret resource is not listable and container env values are redacted). Allowed resources: %s.",
				strings.Join(req.Cluster.Allowed(), ", ")),
			func(ctx context.Context, in listClusterObjectsInput) (anthropic.BetaToolResultBlockParamContentUnion, error) {
				progress(req.Progress, "agent", fmt.Sprintf("inspecting %s.%s in %q", in.Resource, in.Group, orAll(in.Namespace)))
				objects, err := req.Cluster.List(ctx, in.Group, in.Version, in.Resource, in.Namespace)
				if err != nil {
					// Tool errors go back to the model as content so it can
					// adjust course instead of aborting the loop.
					return textResult("error: " + err.Error()), nil
				}
				payload, err := json.Marshal(objects)
				if err != nil {
					return anthropic.BetaToolResultBlockParamContentUnion{}, err
				}
				return textResult(string(payload)), nil
			},
		)
		if err != nil {
			return nil, err
		}
		tools = append(tools, clusterTool)
	}

	progress(req.Progress, "assessing", fmt.Sprintf("analyzing %s %s -> %s against the cluster",
		ref.String(req.Target), req.FromVersion, req.ToVersion))

	prompt, err := buildAssessPrompt(req)
	if err != nil {
		return nil, err
	}

	assessParams := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: responseMaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: assessSystemPrompt}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt)),
		},
	}
	if a.effort != "" {
		assessParams.OutputConfig = anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(a.effort),
		}
	}
	runner := a.client.Beta.Messages.NewToolRunner(tools, anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: assessParams,
		MaxIterations:        assessMaxIterations,
	})
	message, err := runner.RunToCompletion(ctx)
	if err != nil {
		return nil, wrapAPIError(err)
	}

	var text strings.Builder
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	assessment := &kdeltav1.ImpactAssessment{}
	if err := unmarshalModelJSON(text.String(), assessment); err != nil {
		return nil, fmt.Errorf("parsing assessment output: %w", err)
	}
	// Identity fields come from the request, never from the model.
	assessment.Target = req.Target
	assessment.StreamId = req.StreamID
	assessment.FromVersion = req.FromVersion
	assessment.ToVersion = req.ToVersion
	assessment.Provenance = &kdeltav1.Provenance{
		Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_GENERATED,
		Model:       a.model,
		CollectedAt: timestamppb.New(time.Now()),
	}
	return assessment, nil
}

// inventorySummary strips scan payloads down to what assessment needs. Secret
// object refs are withheld: they carry no impact signal and would hand an
// injected instruction exact secret names/namespaces for reconnaissance.
func inventorySummary(scan *kdeltav1.ScanResponse) *kdeltav1.ScanResponse {
	summary := &kdeltav1.ScanResponse{ScannedAt: scan.GetScannedAt(), Edges: scan.GetEdges()}
	for _, r := range scan.GetResources() {
		summary.Resources = append(summary.Resources, &kdeltav1.Resource{
			Ref:        r.GetRef(),
			Upstream:   r.GetUpstream(),
			Streams:    r.GetStreams(),
			Conditions: r.GetConditions(),
			Objects:    nonSecretObjects(r.GetObjects()),
		})
	}
	return summary
}

// nonSecretObjects drops Secret-kind object refs from an inventory listing.
func nonSecretObjects(objects []*kdeltav1.KubernetesObjectRef) []*kdeltav1.KubernetesObjectRef {
	kept := make([]*kdeltav1.KubernetesObjectRef, 0, len(objects))
	for _, obj := range objects {
		if obj.GetApiVersion() == "v1" && obj.GetKind() == "Secret" {
			continue
		}
		kept = append(kept, obj)
	}
	return kept
}

func textResult(text string) anthropic.BetaToolResultBlockParamContentUnion {
	return anthropic.BetaToolResultBlockParamContentUnion{
		OfText: &anthropic.BetaTextBlockParam{Text: text},
	}
}

func messageText(message *anthropic.Message) string {
	var text strings.Builder
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	return text.String()
}

// unmarshalModelJSON parses model output as protobuf JSON, tolerating
// markdown fences and prose around the object.
func unmarshalModelJSON(text string, message proto.Message) error {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON object in model output (%d bytes)", len(text))
	}
	options := protojson.UnmarshalOptions{DiscardUnknown: true}
	return options.Unmarshal([]byte(text[start:end+1]), message)
}

func orAll(namespace string) string {
	if namespace == "" {
		return "all namespaces"
	}
	return namespace
}
