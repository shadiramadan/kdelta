package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// writeStub creates a fake `claude` binary that records its arguments and
// prints the given stdout.
func writeStub(t *testing.T, stdout string) (cliPath, argsPath string) {
	t.Helper()
	dir := t.TempDir()
	argsPath = filepath.Join(dir, "args")
	cliPath = filepath.Join(dir, "claude")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat <<'STUB_EOF'\n%s\nSTUB_EOF\n", argsPath, stdout)
	if err := os.WriteFile(cliPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	return cliPath, argsPath
}

func recordedArgs(t *testing.T, argsPath string) string {
	t.Helper()
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	return string(data)
}

func resultLine(t *testing.T, inner string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": inner,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func TestClaudeCodeExtractChanges(t *testing.T) {
	inner := `{"versions":[{"version":"v1.18.0","changes":[{"type":"CHANGE_TYPE_DEFAULT_CHANGED","summary":"rotationPolicy default flipped"}]}]}`
	cliPath, argsPath := writeStub(t, resultLine(t, inner))

	runner := &ClaudeCode{CLIPath: cliPath, DBPath: "/tmp/db"}
	versions, err := runner.ExtractChanges(context.Background(), ExtractRequest{
		Package:  &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		Releases: []ReleaseNotes{{Version: "v1.18.0", Body: "notes", URL: "https://example.com/v1.18.0"}},
	})
	if err != nil {
		t.Fatalf("ExtractChanges: %v", err)
	}
	if len(versions) != 1 || versions[0].GetChanges()[0].GetType() != kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED {
		t.Fatalf("parsed = %v, want one DEFAULT_CHANGED entry", versions)
	}
	provenance := versions[0].GetProvenance()
	if provenance.GetMethod() != kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_EXTRACTED ||
		provenance.GetModel() != claudeCodeModelLabel ||
		provenance.GetSourceUrl() != "https://example.com/v1.18.0" {
		t.Errorf("provenance = %v, want AI_EXTRACTED via %s with source url", provenance, claudeCodeModelLabel)
	}

	args := recordedArgs(t, argsPath)
	for _, want := range []string{"-p", "--append-system-prompt", "--output-format\njson", "--permission-mode\ndontAsk"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	// Extraction processes attacker-controllable release notes; the built-in
	// tool set must be disabled so an injected instruction has no tools to
	// reach. "--tools" with an empty value is how the CLI disables them.
	if !strings.Contains(args, "--tools\n\n") {
		t.Errorf("extraction must disable built-in tools with --tools \"\":\n%s", args)
	}
}

func TestClaudeCodeAssessImpact(t *testing.T) {
	toolLine := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__kdelta__list_inventory"}]}}`
	inner := `{"overallSeverity":"IMPACT_SEVERITY_HIGH","summary":"stub assessment","resources":[{"ref":{"detector":"helm","namespace":"cert-manager","name":"cert-manager"},"severity":"IMPACT_SEVERITY_HIGH","explanation":"target"}]}`
	cliPath, argsPath := writeStub(t, toolLine+"\n"+resultLine(t, inner))

	var stages []string
	runner := &ClaudeCode{CLIPath: cliPath, DBPath: "/tmp/db", SelfPath: "/usr/bin/kdelta-test"}
	assessment, err := runner.AssessImpact(context.Background(), AssessRequest{
		Target:      &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		StreamID:    "app",
		FromVersion: "v1.17.2",
		ToVersion:   "v1.21.1",
		ChangeSet:   &kdeltav1.ChangeSet{FromVersion: "v1.17.2", ToVersion: "v1.21.1"},
		Inventory:   &kdeltav1.ScanResponse{},
		Progress:    func(stage, message string) { stages = append(stages, stage+": "+message) },
	})
	if err != nil {
		t.Fatalf("AssessImpact: %v", err)
	}
	if assessment.GetOverallSeverity() != kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH {
		t.Errorf("severity = %v, want HIGH", assessment.GetOverallSeverity())
	}
	if assessment.GetStreamId() != "app" || assessment.GetFromVersion() != "v1.17.2" {
		t.Errorf("identity fields not enforced: %v", assessment)
	}
	if assessment.GetProvenance().GetMethod() != kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_GENERATED {
		t.Errorf("provenance = %v, want AI_GENERATED", assessment.GetProvenance())
	}

	joined := strings.Join(stages, "\n")
	if !strings.Contains(joined, "agent: using list_inventory") {
		t.Errorf("tool-use progress missing:\n%s", joined)
	}

	args := recordedArgs(t, argsPath)
	for _, want := range []string{
		"--output-format\nstream-json",
		"--mcp-config",
		"/usr/bin/kdelta-test",
		"agent-tools",
		"--allowedTools\nmcp__kdelta__list_inventory,mcp__kdelta__list_cluster_objects",
		// The agent reaches the cluster ONLY through the two restricted MCP
		// tools: built-ins disabled, and host MCP configs ignored so no other
		// server can be injected via user/project settings.
		"--tools\n\n",
		"--strict-mcp-config",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
}

func TestClaudeCodeCredentialErrors(t *testing.T) {
	line, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "error_during_execution", "is_error": true,
		"result": "Invalid API key · Please run /login",
	})
	if err != nil {
		t.Fatal(err)
	}
	cliPath, _ := writeStub(t, string(line))

	runner := &ClaudeCode{CLIPath: cliPath, DBPath: "/tmp/db"}
	_, err = runner.ExtractChanges(context.Background(), ExtractRequest{
		Package:  &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		Releases: []ReleaseNotes{{Version: "v1.18.0", Body: "notes"}},
	})
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("error = %v, want ErrNoCredentials", err)
	}
}
