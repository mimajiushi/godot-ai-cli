package update

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/testutil/fakegithub"
)

// stubOpsSeams swaps the queryOps/currentOps seams and restores them.
func stubOpsSeams(t *testing.T, query func(ctx context.Context, exe string) ([]string, error), current func() []string) {
	t.Helper()
	oldQuery, oldCurrent := queryOps, currentOps
	queryOps, currentOps = query, current
	t.Cleanup(func() { queryOps, currentOps = oldQuery, oldCurrent })
}

// runUpdateWithStubs drives the happy-path update against a fake install
// dir with the ops seams stubbed, and returns the result map.
func runUpdateWithStubs(t *testing.T, query func(ctx context.Context, exe string) ([]string, error), current func() []string) map[string]any {
	t.Helper()
	stubOpsSeams(t, query, current)

	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	server := fakegithub.New(t, "v0.2.0", map[string][]byte{
		"godot-ai-cli-0.2.0-windows-amd64.zip": zipData,
		"godot-ai-cli-0.2.0-checksums.txt":     fakegithub.Checksums("godot-ai-cli-0.2.0-windows-amd64.zip", zipData),
	})
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		GOOS:           "windows",
		GOARCH:         "amd64",
		InstallDir:     dir,
		AssumeYes:      true,
		PromptOut:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "updated" {
		t.Fatalf("status = %v", result["status"])
	}
	return result
}

// TestDiffOpNames: added/removed sets, sorted, both directions.
func TestDiffOpNames(t *testing.T) {
	added, removed := diffOpNames(
		[]string{"node create", "editor screenshot", "logs read"},
		[]string{"editor record", "node create", "editor screenshot"},
	)
	if len(added) != 1 || added[0] != "editor record" {
		t.Errorf("added = %v", added)
	}
	if len(removed) != 1 || removed[0] != "logs read" {
		t.Errorf("removed = %v", removed)
	}
	added, removed = diffOpNames([]string{"a"}, []string{"a"})
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("unchanged surface: added = %v, removed = %v", added, removed)
	}
}

// TestDocsHintOnSurfaceChange: a changed op surface yields ops_added /
// ops_removed plus the regeneration instructions.
func TestDocsHintOnSurfaceChange(t *testing.T) {
	stubOpsSeams(t,
		func(context.Context, string) ([]string, error) {
			return []string{"node create", "editor record"}, nil
		},
		func() []string { return []string{"node create", "logs read"} },
	)
	hint := docsHint(context.Background(), "unused")
	if hint == nil {
		t.Fatal("changed surface must produce a docs hint")
	}
	added, ok := hint["ops_added"].([]string)
	if !ok || len(added) != 1 || added[0] != "editor record" {
		t.Errorf("ops_added = %v", hint["ops_added"])
	}
	removed, ok := hint["ops_removed"].([]string)
	if !ok || len(removed) != 1 || removed[0] != "logs read" {
		t.Errorf("ops_removed = %v", hint["ops_removed"])
	}
	if hint["message"] != docsHintMessage {
		t.Errorf("message = %v", hint["message"])
	}
}

// TestDocsHintNilWhenUnchanged: a version bump without op changes needs no
// doc sync, so no hint at all.
func TestDocsHintNilWhenUnchanged(t *testing.T) {
	same := []string{"node create", "editor screenshot"}
	stubOpsSeams(t,
		func(context.Context, string) ([]string, error) { return same, nil },
		func() []string { return same },
	)
	if hint := docsHint(context.Background(), "unused"); hint != nil {
		t.Errorf("unchanged surface must not hint, got %v", hint)
	}
}

// TestDocsHintOnQueryFailure: an unqueryable new binary still earns a
// hint — the docs may have drifted, the diff is just unavailable.
func TestDocsHintOnQueryFailure(t *testing.T) {
	stubOpsSeams(t,
		func(context.Context, string) ([]string, error) { return nil, errors.New("exec failed") },
		func() []string { return nil },
	)
	hint := docsHint(context.Background(), "unused")
	if hint == nil {
		t.Fatal("query failure must still produce a docs hint")
	}
	if hint["ops_diff_error"] != "exec failed" {
		t.Errorf("ops_diff_error = %v", hint["ops_diff_error"])
	}
	if _, ok := hint["ops_added"]; ok {
		t.Errorf("failed diff must not claim ops_added: %v", hint)
	}
}

// TestRunAttachesDocsHint: the full update flow surfaces the hint in its
// JSON result when the op surface changed.
func TestRunAttachesDocsHint(t *testing.T) {
	result := runUpdateWithStubs(t,
		func(context.Context, string) ([]string, error) {
			return []string{"node create", "editor record"}, nil
		},
		func() []string { return []string{"node create"} },
	)
	hint, ok := result["docs_hint"].(map[string]any)
	if !ok {
		t.Fatalf("docs_hint missing from result: %v", result)
	}
	added, ok := hint["ops_added"].([]string)
	if !ok || len(added) != 1 || added[0] != "editor record" {
		t.Errorf("ops_added = %v", hint["ops_added"])
	}
}

// TestRunOmitsDocsHintWhenUnchanged: no surface change, no hint key.
func TestRunOmitsDocsHintWhenUnchanged(t *testing.T) {
	same := []string{"node create"}
	result := runUpdateWithStubs(t,
		func(context.Context, string) ([]string, error) { return same, nil },
		func() []string { return same },
	)
	if _, ok := result["docs_hint"]; ok {
		t.Errorf("unchanged surface must not attach docs_hint: %v", result)
	}
}
