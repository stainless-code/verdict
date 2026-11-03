package controls

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const apiBase = "0123456789abcdef0123456789abcdef01234567"

// apiFake plays a checkout that cannot supply the base (every git call
// fails) and a GitHub contents API answering from canned bodies. A path
// absent from bodies is a 404; one in fail answers that stderr.
type apiFake struct {
	bodies map[string]string
	fail   map[string]string
	calls  []string
}

func (f *apiFake) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	if cmd.Argv[0] == "git" {
		return core.Result{Stderr: "fatal: not a git repository", ExitCode: 128}, errors.New("exit status 128")
	}
	endpoint := cmd.Argv[2]
	f.calls = append(f.calls, endpoint)
	path, _, _ := strings.Cut(strings.TrimPrefix(endpoint, "repos/acme/app/contents/"), "?")
	if stderr, ok := f.fail[path]; ok {
		return core.Result{Stderr: stderr, ExitCode: 1}, errors.New("exit status 1")
	}
	body, ok := f.bodies[path]
	if !ok {
		return core.Result{Stderr: "gh: Not Found (HTTP 404)", ExitCode: 1}, errors.New("exit status 1")
	}
	return core.Result{Stdout: body}, nil
}

// fileBody renders a contents-API file response, base64 wrapped at 60
// columns the way GitHub does.
func fileBody(content string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	var wrapped []string
	for len(enc) > 60 {
		wrapped, enc = append(wrapped, enc[:60]), enc[60:]
	}
	wrapped = append(wrapped, enc)
	return fmt.Sprintf(`{"type":"file","encoding":"base64","size":%d,"content":%q}`,
		len(content), strings.Join(wrapped, "\n")+"\n")
}

func apiResolve(t *testing.T, f *apiFake, repo string) (*Snapshot, *core.RefusalError) {
	t.Helper()
	return Resolve(t.Context(), f, Request{BundleDir: filepath.Join(t.TempDir(), "12"), Repo: repo, BaseSHA: apiBase})
}

// TestResolveContentsFallback proves present and absent controls land in the
// snapshot at the API's base bytes.
func TestResolveContentsFallback(t *testing.T) {
	_, snap, want := contentsFallback(t)
	if snap.File.Policy != PolicyBase || snap.File.SHA != apiBase || snap.Used() != 3 {
		t.Fatalf("file = %+v", snap.File)
	}
	for path, content := range want {
		if got, ok := snap.ReadFile(path); !ok || string(got) != content {
			t.Errorf("%s = %q (ok=%v), want %q", path, got, ok, content)
		}
	}
	if snap.Has(".review/learnings.md") {
		t.Error("a 404 control became used")
	}
}

// TestResolveContentsFallbackPinsBase proves the API read asks at the full
// base SHA and the resulting snapshot passes the gate.
func TestResolveContentsFallbackPinsBase(t *testing.T) {
	f, snap, _ := contentsFallback(t)
	if len(f.calls) != len(Inventory) || !strings.HasSuffix(f.calls[0], "?ref="+apiBase) {
		t.Errorf("calls = %q", f.calls)
	}
	if _, ref := Gate(filepath.Dir(snap.Dir), apiBase); ref != nil {
		t.Errorf("Gate rejected the API snapshot: %v", ref)
	}
}

// contentsFallback resolves the fallback fixture and returns the fake, the
// snapshot, and the bytes each control must carry.
func contentsFallback(t *testing.T) (*apiFake, *Snapshot, map[string]string) {
	t.Helper()
	long := strings.Repeat("a rule that wraps the base64 lines\n", 20)
	f := &apiFake{bodies: map[string]string{
		".review/panel":    fileBody("base-model\n"),
		".review/kb.jsonl": fileBody(long),
		".review/budget":   fileBody(""),
	}}
	snap, ref := apiResolve(t, f, "acme/app")
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return f, snap, map[string]string{".review/panel": "base-model\n", ".review/kb.jsonl": long, ".review/budget": ""}
}

// TestResolveContentsRefusals proves the caps, non-file shapes, bad payloads,
// and every non-404 failure refuse typed, and a missing repo never calls gh.
func TestResolveContentsRefusals(t *testing.T) {
	over := fmt.Sprintf(`{"type":"file","encoding":"base64","size":%d,"content":""}`, MaxFileBytes+1)
	big := fileBody(strings.Repeat("x", MaxFileBytes))
	totalOver := map[string]string{}
	for _, p := range Inventory[:MaxTotalBytes/MaxFileBytes+1] {
		totalOver[p] = big
	}
	for _, tc := range []struct {
		name, code   string
		bodies, fail map[string]string
		repo         string
	}{
		{"per-file cap on size before decode", CodeTooLarge, map[string]string{".review/panel": over}, nil, "acme/app"},
		{"total cap", CodeTooLarge, totalOver, nil, "acme/app"},
		{"directory answers an array", CodeUnavailable, map[string]string{".review/panel": `[{"type":"file"}]`}, nil, "acme/app"},
		{"symlink type", CodeUnavailable, map[string]string{".review/panel": `{"type":"symlink","size":3}`}, nil, "acme/app"},
		{"undecodable base64", CodeUnavailable, map[string]string{".review/panel": `{"type":"file","encoding":"base64","size":3,"content":"@@@"}`}, nil, "acme/app"},
		{"size mismatch", CodeUnavailable, map[string]string{".review/panel": `{"type":"file","encoding":"base64","size":9,"content":"YWJj"}`}, nil, "acme/app"},
		{"non-base64 encoding", CodeUnavailable, map[string]string{".review/panel": `{"type":"file","encoding":"none","size":3,"content":""}`}, nil, "acme/app"},
		{"server error is not absence", CodeUnavailable, nil, map[string]string{".review/budget": "gh: Server Error (HTTP 500)"}, "acme/app"},
		{"unknown ref is not absence", CodeUnavailable, nil, map[string]string{".review/panel": "gh: No commit found for the ref (HTTP 422)"}, "acme/app"},
		{"unsafe repo slug", CodeUnavailable, nil, nil, "acme/../app"},
	} {
		f := &apiFake{bodies: tc.bodies, fail: tc.fail}
		snap, ref := apiResolve(t, f, tc.repo)
		if ref == nil || ref.Code != tc.code || snap != nil {
			t.Errorf("%s: snap = %+v, ref = %+v, want %s", tc.name, snap, ref, tc.code)
		}
	}
	f := &apiFake{bodies: map[string]string{".review/panel": fileBody("x")}}
	if _, ref := apiResolve(t, f, ""); ref == nil || ref.Code != CodeUnavailable || len(f.calls) != 0 {
		t.Errorf("no repo: ref = %+v, gh calls = %q", ref, f.calls)
	}
}

// TestResolveContentsLeavesNothingOnRefusal proves a refused API read writes
// no controls.json.
func TestResolveContentsLeavesNothingOnRefusal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "12")
	f := &apiFake{fail: map[string]string{".review/panel": "gh: boom (HTTP 500)"}}
	if _, ref := Resolve(t.Context(), f, Request{BundleDir: dir, Repo: "acme/app", BaseSHA: apiBase}); ref == nil {
		t.Fatal("want a refusal")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Error("a refused resolve left a controls.json behind")
	}
}
