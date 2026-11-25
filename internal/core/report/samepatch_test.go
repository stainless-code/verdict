package reportcore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/gitcmd"
)

const samePatchHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const samePatchBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const samePatchID = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// samePatchDiff is the canonical reviewed diff bytes every coherent
// same-patch fixture binds.
const samePatchDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// samePatchStubCommander answers `git patch-id --stable` for the fixture
// diff and fails anything else exactly like an unavailable git.
type samePatchStubCommander struct{}

func (c *samePatchStubCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if _, args, ok := gitcmd.Split(cmd.Argv); ok && slices.Equal(args, []string{"patch-id", "--stable"}) && cmd.Stdin != nil {
		raw, err := io.ReadAll(cmd.Stdin)
		if err == nil && string(raw) == samePatchDiff {
			return core.Result{Stdout: samePatchID + " 0000000000000000000000000000000000000000\n"}, nil
		}
	}
	return core.Result{ExitCode: 1}, errors.New("cannot compute patch id")
}

// withIdentityCommander points the report request at the canned patch-id
// runner so verified identities recompute.
func withIdentityCommander(req core.ReadRequest) core.ReadRequest {
	req.Commander = &samePatchStubCommander{}
	return req
}

// samePatchBundle writes a byte-coherent bundle whose fetched head moved by
// rebase but whose patch id matches the completed post receipt stamped at
// the posted head.
func samePatchBundle(t *testing.T, postedPatch string) {
	t.Helper()
	writeFile(t, ".verdict/state/5101/full.diff", samePatchDiff)
	sum := sha256.Sum256([]byte(samePatchDiff))
	sha := hex.EncodeToString(sum[:])
	writeFile(t, ".verdict/state/5101/identity.json",
		`{"schema_version":"1","patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"`+sha+`","raw_diff_sha256":"`+sha+`","head_sha":"`+storeHeadSHA+`","base_sha":"`+samePatchBase+`"}`)
	post := `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"head":"` + samePatchHead + `",` +
		`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + postedPatch + `","patch_id_kind":"git-patch-id"}`
	writeFile(t, ".verdict/state/5101/post.json", post)
}

// TestRunReportCompletenessSamePatch proves the rebase pass: posted and
// fetched heads differ but the patch ids agree, so the head dimension passes
// naming both heads as a rebase.
func TestRunReportCompletenessSamePatch(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("GateFailed = true; same patch should pass\n%s", dimSummary(resp))
	}
	requireDims(t, resp, wantDim{"head", "pass", "same patch reviewed at aaaaaaa (posted bbbbbbb)"})
}

// TestRunReportCompletenessDifferentPatch proves a changed patch still
// fails stale even when both sides stamp patch ids.
func TestRunReportCompletenessDifferentPatch(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, "0123456789abcdef0123456789abcdef01234567")
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false; a different patch must fail")
	}
	requireDims(t, resp, wantDim{id: "head", status: "fail"})
	requireDimDetail(t, resp, "head", "stale-head", "posted HEAD bbbbbbb != fetched HEAD aaaaaaa")
}

// TestRunReportCompletenessLegacyWarns proves a bundle without identity
// falls back to the SHA comparison and warns.
func TestRunReportCompletenessLegacyWarns(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	writeFile(t, ".verdict/state/5101/post.json",
		`{"schema_version":"1","review":{"id":1},"head":"`+storeHeadSHA+`","posted_at":"2026-01-01T00:00:00Z"}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Errorf("GateFailed = true; matching heads should pass\n%s", dimSummary(resp))
	}
	requireDims(t, resp, wantDim{"head", "pass", "HEAD aaaaaaa reviewed (post matches fetch)"})
	if !slices.Contains(resp.Warnings, "patch identity unavailable — completeness compares heads") {
		t.Errorf("warnings = %q, want the legacy fallback warning", resp.Warnings)
	}
}

// assertStaleCompleteness proves the gate fails stale with no same-patch
// relaxation: the head dimension fails stale-head.
func assertStaleCompleteness(t *testing.T, resp *reportcore.Response, warn string) {
	t.Helper()
	if resp == nil {
		t.Fatal("missing response")
	}
	if !resp.GateFailed {
		t.Fatalf("GateFailed = false, want the stale-head failure\n%s", dimSummary(resp))
	}
	requireDims(t, resp, wantDim{id: "head", status: "fail"})
	requireDimDetail(t, resp, "head", "stale-head")
	if warn != "" && !slices.Contains(resp.Warnings, warn) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, warn)
	}
}

// TestRunReportCompletenessStaleBytes proves an identity that no longer
// binds the bundle bytes never relaxes the gate: the rebase line stays
// off and the SHA comparison fails stale with the legacy warning.
func TestRunReportCompletenessStaleBytes(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	writeFile(t, ".verdict/state/5101/full.diff", "replaced bytes\n")
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

// TestRunReportCompletenessStaleHead proves an identity naming a moved head
// never relaxes the gate.
func TestRunReportCompletenessStaleHead(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	raw := readFile(t, ".verdict/state/5101/identity.json")
	writeFile(t, ".verdict/state/5101/identity.json",
		strings.Replace(string(raw), `"head_sha":"`+storeHeadSHA+`"`, `"head_sha":"cccccccccccccccccccccccccccccccccccccccc"`, 1))
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

// TestRunReportCompletenessIncompleteReceipt proves a two-field post stub
// proves no publication: the same-patch relaxation never applies and the
// pre-existing incomplete-post gate stands (no head recorded).
func TestRunReportCompletenessIncompleteReceipt(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	writeFile(t, ".verdict/state/5101/post.json",
		`{"patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id"}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatalf("GateFailed = false, want the incomplete-post failure\n%s", dimSummary(resp))
	}
	assertIncompletePostGate(t, resp)
}

// assertIncompletePostGate proves the pre-existing incomplete-post gate
// stands with no same-patch relaxation: the head dimension reads unknown and
// names the corrupt post.
func assertIncompletePostGate(t *testing.T, resp *reportcore.Response) {
	t.Helper()
	requireDims(t, resp, wantDim{id: "head", status: "unknown"})
	requireDimDetail(t, resp, "head", "post record carries no head SHA", "corrupt-post")
}

// TestRunReportCompletenessPendingReceipt proves a pending or dry-run post
// receipt proves no publication.
func TestRunReportCompletenessPendingReceipt(t *testing.T) {
	for name, review := range map[string]string{
		"pending": `{"schema_version":"1","review":{"id":1,"state":"PENDING"},"head":"` + samePatchHead + `",` +
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + samePatchID + `","patch_id_kind":"git-patch-id"}`,
		"dry-run": `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"dry_run":true,"head":"` + samePatchHead + `",` +
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + samePatchID + `","patch_id_kind":"git-patch-id"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := withIdentityCommander(fixture(t, "[]"))
			samePatchBundle(t, samePatchID)
			writeFile(t, ".verdict/state/5101/post.json", review)
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
				ReadRequest: req,
				Flags:       reportcore.Flags{Completeness: true},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			assertStaleCompleteness(t, resp, "")
		})
	}
}

// TestRunReportCompletenessInvalidIdentity proves an algorithm-invalid
// patch id warns and falls back to the SHA comparison.
func TestRunReportCompletenessInvalidIdentity(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	raw := readFile(t, ".verdict/state/5101/identity.json")
	writeFile(t, ".verdict/state/5101/identity.json", strings.Replace(string(raw), samePatchID, "not-a-patch-id", 1))
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRunReportCompletenessTamperedClaim proves a hex-valid patch claim over
// the wrong bytes never relaxes the gate: coherent hashes and head plus a
// matching historical receipt still fail stale with the fallback warning,
// because only the recomputed algorithm binds the claim.
func TestRunReportCompletenessTamperedClaim(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	tampered := strings.Repeat("1", 40)
	ident := readFile(t, ".verdict/state/5101/identity.json")
	writeFile(t, ".verdict/state/5101/identity.json", strings.Replace(string(ident), samePatchID, tampered, 1))
	posted := readFile(t, ".verdict/state/5101/post.json")
	writeFile(t, ".verdict/state/5101/post.json", strings.Replace(string(posted), samePatchID, tampered, 1))
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

// TestRunReportCompletenessEmptyReceipt proves an empty review object proves
// no publication even when head, time, and patch fields are well-formed.
func TestRunReportCompletenessEmptyReceipt(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	writeFile(t, ".verdict/state/5101/post.json",
		`{"schema_version":"1","review":{},"head":"`+samePatchHead+`","posted_at":"2026-01-01T00:00:00Z",`+
			`"patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id"}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "")
}

// TestRunReportCompletenessInvalidHeadTime proves non-head/non-time receipt
// metadata proves no publication.
func TestRunReportCompletenessInvalidHeadTime(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	writeFile(t, ".verdict/state/5101/post.json",
		`{"schema_version":"1","review":{},"head":"not-a-head","posted_at":"not-a-time",`+
			`"patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id"}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "")
}

// TestRunReportCompletenessRawUnreadable proves a present-but-unreadable
// raw diff invalidates the verified claim instead of certifying the
// filtered bytes.
func TestRunReportCompletenessRawUnreadable(t *testing.T) {
	req := withIdentityCommander(fixture(t, "[]"))
	samePatchBundle(t, samePatchID)
	if err := os.Mkdir(".verdict/state/5101/full.raw.diff", 0o755); err != nil {
		t.Fatal(err)
	}
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}
