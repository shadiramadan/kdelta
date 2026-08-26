package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/ref"
)

// ClaudeCode runs flows through the Claude Agent SDK harness by driving the
// `claude` CLI as a subprocess (`-p`, the documented path for hosts without a
// native Agent SDK). Authentication follows the CLI's own precedence: the
// machine's `/login` subscription credential, CLAUDE_CODE_OAUTH_TOKEN (from
// `claude setup-token`) in servers/CI, or ANTHROPIC_API_KEY when set (which
// outranks the subscription). The agent's restricted tools are served back to
// the harness over MCP by the hidden `kdelta agent-tools` command;
// AssessRequest.Cluster is therefore unused here.
type ClaudeCode struct {
	// CLIPath overrides the `claude` binary; tests point it at a stub.
	CLIPath string
	// DBPath is handed to the agent-tools MCP subprocess so it reads the
	// same cache.
	DBPath string
	// SelfPath overrides the kdelta executable spawned for agent-tools.
	SelfPath string
	// Effort overrides the harness's reasoning-effort level (low, medium,
	// high, xhigh, max); empty keeps the CLI's default.
	Effort string
}

const claudeCodeModelLabel = "claude-agent-sdk"

// NewClaudeCode builds the CLI-backed runner, honoring KDELTA_EFFORT.
func NewClaudeCode(dbPath string) *ClaudeCode {
	return &ClaudeCode{DBPath: dbPath, Effort: effortFromEnv()}
}

func (c *ClaudeCode) cli() string {
	if c.CLIPath != "" {
		return c.CLIPath
	}
	return "claude"
}

// cliResult is the final payload of `claude -p --output-format json` and the
// terminal line of stream-json.
type cliResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// wrapCLIError distinguishes credential problems from other failures.
func wrapCLIError(detail string, err error) error {
	lowered := strings.ToLower(detail)
	for _, marker := range []string{"login", "api key", "authentication", "credential", "oauth"} {
		if strings.Contains(lowered, marker) {
			return fmt.Errorf("%w (%s)", ErrNoCredentials, strings.TrimSpace(detail))
		}
	}
	if err != nil {
		return fmt.Errorf("running claude CLI: %w (%s)", err, strings.TrimSpace(detail))
	}
	return fmt.Errorf("claude CLI failed: %s", strings.TrimSpace(detail))
}

// run executes one non-interactive harness invocation from a scratch working
// directory (so no repository context leaks into the session).
func (c *ClaudeCode) run(ctx context.Context, prompt string, extraArgs []string, onLine func([]byte)) (*cliResult, error) {
	dir, err := os.MkdirTemp("", "kdelta-agent-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	args := []string{"-p", prompt, "--permission-mode", "dontAsk"}
	if c.Effort != "" {
		args = append(args, "--effort", c.Effort)
	}
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, c.cli(), args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, wrapCLIError(stderr.String(), err)
	}

	var final *cliResult
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if onLine != nil {
			onLine(line)
		}
		var candidate cliResult
		if err := json.Unmarshal(line, &candidate); err == nil && candidate.Type == "result" {
			result := candidate
			final = &result
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return nil, scanErr
	}
	if final == nil {
		return nil, wrapCLIError(stderr.String(), waitErr)
	}
	if final.IsError || waitErr != nil {
		return nil, wrapCLIError(final.Result+" "+stderr.String(), waitErr)
	}
	return final, nil
}

func (c *ClaudeCode) ExtractChanges(ctx context.Context, req ExtractRequest) ([]*kdeltav1.VersionChanges, error) {
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
		result, err := c.run(ctx, prompt, []string{
			"--append-system-prompt", extractSystemPrompt,
			"--output-format", "json",
			// Extraction is pure text structuring over attacker-controllable
			// release notes: disable the entire built-in tool set so an
			// injected instruction has no Bash/WebFetch/Read to reach. --tools
			// (not --permission-mode) is what gates tool availability; without
			// it dontAsk would proceed to run any built-in tool unprompted.
			"--tools", "",
		}, nil)
		if err != nil {
			return nil, err
		}
		var set kdeltav1.ChangeSet
		if err := unmarshalModelJSON(result.Result, &set); err != nil {
			return nil, fmt.Errorf("parsing extraction output: %w", err)
		}
		for _, vc := range set.GetVersions() {
			vc.Provenance = &kdeltav1.Provenance{
				Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_EXTRACTED,
				Model:       claudeCodeModelLabel,
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

// streamEvent is the subset of stream-json lines used for progress.
type streamEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

func (c *ClaudeCode) AssessImpact(ctx context.Context, req AssessRequest) (*kdeltav1.ImpactAssessment, error) {
	self := c.SelfPath
	if self == "" {
		var err error
		if self, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("resolving kdelta executable for agent-tools: %w", err)
		}
	}
	mcpConfig, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"kdelta": map[string]any{
				"command": self,
				"args":    []string{"agent-tools", "--db", c.DBPath},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	prompt, err := buildAssessPrompt(req)
	if err != nil {
		return nil, err
	}
	progress(req.Progress, "assessing", fmt.Sprintf("analyzing %s %s -> %s against the cluster",
		ref.String(req.Target), req.FromVersion, req.ToVersion))

	onLine := func(line []byte) {
		var event streamEvent
		if json.Unmarshal(line, &event) != nil || event.Type != "assistant" {
			return
		}
		for _, block := range event.Message.Content {
			if block.Type == "tool_use" {
				progress(req.Progress, "agent", "using "+strings.TrimPrefix(block.Name, "mcp__kdelta__"))
			}
		}
	}

	result, err := c.run(ctx, prompt, []string{
		"--append-system-prompt", assessSystemPrompt,
		"--output-format", "stream-json",
		"--verbose",
		"--mcp-config", string(mcpConfig),
		// Disable every built-in tool (Bash/WebFetch/Read/ToolSearch/…): the
		// agent must reach the cluster ONLY through the two restricted,
		// no-secrets MCP tools below. --allowedTools alone does not do this —
		// it governs MCP prompting, leaving the built-in set usable, which is
		// how ToolSearch ran in earlier assessments.
		"--tools", "",
		// Ignore any user/project MCP servers on the host; load only ours.
		"--strict-mcp-config",
		"--allowedTools", "mcp__kdelta__list_inventory,mcp__kdelta__list_cluster_objects",
	}, onLine)
	if err != nil {
		return nil, err
	}

	assessment := &kdeltav1.ImpactAssessment{}
	if err := unmarshalModelJSON(result.Result, assessment); err != nil {
		return nil, fmt.Errorf("parsing assessment output: %w", err)
	}
	assessment.Target = req.Target
	assessment.StreamId = req.StreamID
	assessment.FromVersion = req.FromVersion
	assessment.ToVersion = req.ToVersion
	assessment.Provenance = &kdeltav1.Provenance{
		Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_GENERATED,
		Model:       claudeCodeModelLabel,
		CollectedAt: timestamppb.New(time.Now()),
	}
	return assessment, nil
}
