package fetchcore

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/retry"
)

// TestFetchRetriesInternalError proves fetch now shares post's transient set:
// gh's internal-error retry note runs the full attempt budget instead of
// failing on the first call.
func TestFetchRetriesInternalError(t *testing.T) {
	t.Parallel()
	const argv = "gh\x00api\x00user"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stderr: "HTTP 500: Internal error occurred, please try again", fail: true},
	}}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings}
	if _, err := s.ghFetch(context.Background(), "api", "user"); err == nil {
		t.Fatal("exhausted retry proceeded")
	}
	if len(cmdr.calls) != retry.MaxAttempts {
		t.Errorf("calls = %d, want %d", len(cmdr.calls), retry.MaxAttempts)
	}
	if len(warnings) != retry.MaxAttempts-1 {
		t.Fatalf("warnings = %q, want %d retry notices", warnings, retry.MaxAttempts-1)
	}
	for i, warning := range warnings {
		if want := fmt.Sprintf("retry %d/2", i+1); !strings.Contains(warning, want) {
			t.Errorf("warning %d = %q, want %s", i, warning, want)
		}
	}
}

// TestFetchDoesNotRetryPlainFailure proves a plain auth failure still fails
// on the first attempt: only the shared transient set retries.
func TestFetchDoesNotRetryPlainFailure(t *testing.T) {
	t.Parallel()
	const argv = "gh\x00api\x00user"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stderr: "HTTP 403: Resource not accessible by integration", fail: true},
	}}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings}
	if _, err := s.ghFetch(context.Background(), "api", "user"); err == nil {
		t.Fatal("plain auth failure proceeded")
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(cmdr.calls))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}
