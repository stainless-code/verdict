package fetchcore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
)

// embeddedReviewBytes builds the bundle Procedure from the embedded payload
// paths directly, so a dropped or reordered assembly file cannot pass by
// feeding the test its own expectation.
func embeddedReviewBytes(t *testing.T) []byte {
	t.Helper()
	var out []byte
	for i, name := range reviewPayloadPaths {
		if i > 0 {
			out = append(out, '\n')
		}
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		out = append(out, raw...)
	}
	return out
}

// fetchWithProcedure runs one hermetic fetch of either kind and returns its
// response and the bundle dir in display form.
func fetchWithProcedure(t *testing.T, kind string, show bool) (*core.FetchResponse, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	var human, diag bytes.Buffer
	var warnings []string
	var resp *core.FetchResponse
	var err error
	var dir string
	switch kind {
	case "fetch":
		req := fetchTestReq(fetchHappyCommander(t), &human, &diag, &warnings)
		req.Flags.ShowPrompts = show
		resp, err = Run(t.Context(), req)
		dir = filepath.ToSlash(filepath.Join(".verdict", "state", "7"))
	case "fetch-local":
		req := localScriptReq(stagedLocalCommander(t, cleanStagedDiff), &human, &diag, &warnings,
			nil, core.FetchLocalFlags{Staged: true, ShowPrompts: show})
		resp, err = RunLocal(t.Context(), req)
		dir = filepath.ToSlash(filepath.Join(".verdict", "state", "local-mine"))
	default:
		t.Fatalf("unknown fetch kind %q", kind)
	}
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return resp, dir
}

func assertBundleRef(t *testing.T, dir string, ref skillfs.HandoffRef) {
	t.Helper()
	path := filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(ref.Path))
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.ToSlash(path), err)
	}
	want, err := skillfs.ReadRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not a byte-identical copy of %s", filepath.ToSlash(path), ref.Source)
	}
}

func assertBundleProcedure(t *testing.T, dir string, want []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.FromSlash(dir), "procedure.md"))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.ToSlash(filepath.Join(dir, "procedure.md")), err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("%s/procedure.md is not the embedded assembly", dir)
	}
	for _, ref := range skillfs.HandoffRefs() {
		assertBundleRef(t, dir, ref)
	}
}

func assertDumpStartsWithProcedure(t *testing.T, kind, dir string, resp *core.FetchResponse, want []byte) {
	t.Helper()
	if resp.Data == nil || resp.Data.PromptDump == nil {
		t.Fatalf("%s returned no prompt dump", kind)
	}
	if !strings.HasPrefix(*resp.Data.PromptDump, string(want)) {
		t.Fatalf("%s dump does not start with %s bytes", kind, dir)
	}
}

// TestFetchBundlesCarryProcedureFromBinary proves both fetch kinds persist the
// binary's review Procedure under the same atomic writer as their other files.
func TestFetchBundlesCarryProcedureFromBinary(t *testing.T) {
	want := embeddedReviewBytes(t)
	for _, kind := range []string{"fetch", "fetch-local"} {
		t.Run(kind, func(t *testing.T) {
			_, dir := fetchWithProcedure(t, kind, false)
			assertBundleProcedure(t, dir, want)
		})
	}
}

// TestShowPromptsDumpStartsWithBundleProcedure pins the prefix contract: the
// printed dump opens with exactly the bytes written into the bundle, before
// the trust boundary and the untrusted fences.
func TestShowPromptsDumpStartsWithBundleProcedure(t *testing.T) {
	want := embeddedReviewBytes(t)
	for _, kind := range []string{"fetch", "fetch-local"} {
		t.Run(kind, func(t *testing.T) {
			resp, dir := fetchWithProcedure(t, kind, true)
			assertDumpStartsWithProcedure(t, kind, dir, resp, want)
		})
	}
}

// withMissingReviewProcedure swaps the smallest seam in fetch: a package-level
// func var that tests set to simulate a broken embedded payload. Production
// always calls skillfs.ReviewProcedure through the same var.
func withMissingReviewProcedure(t *testing.T, path string) {
	t.Helper()
	saved := reviewProcedure
	t.Cleanup(func() { reviewProcedure = saved })
	reviewProcedure = func(skillfs.HandoffFacts) ([]byte, error) {
		return nil, &skillfs.ProcedureFileError{Cause: fs.ErrNotExist, Path: path}
	}
}

// TestMissingEmbeddedReviewProcedureRefusesFetchLocal proves both the preamble
// and a sibling payload file refuse typed through the real fetch-local path,
// and that a partial Procedure never lands in the bundle.
func TestMissingEmbeddedReviewProcedureRefusesFetchLocal(t *testing.T) {
	for _, path := range []string{"prompts/handoff/role-method.md", "prompts/handoff/capability-map.md"} {
		t.Run(path, func(t *testing.T) { assertMissingReviewProcedureRefusal(t, path) })
	}
}

func assertMissingReviewProcedureRefusal(t *testing.T, path string) {
	t.Helper()
	withMissingReviewProcedure(t, path)
	t.Chdir(t.TempDir())
	var human, diag bytes.Buffer
	var warnings []string
	req := localScriptReq(stagedLocalCommander(t, cleanStagedDiff), &human, &diag, &warnings,
		nil, core.FetchLocalFlags{Staged: true, ShowPrompts: true})
	_, err := RunLocal(t.Context(), req)
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok || ref.Code != "prompt-dump-failed" {
		t.Fatalf("refusal = %+v (%v), want prompt-dump-failed", ref, err)
	}
	if !strings.Contains(ref.Message, path) {
		t.Errorf("message = %q, want it to name %s", ref.Message, path)
	}
	procedure := filepath.Join(".verdict", "state", "local-mine", "procedure.md")
	if _, statErr := os.Stat(procedure); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("partial procedure.md exists after refusal: %v", statErr)
	}
}
