package recordcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// The three candidate findings every validation case starts from: a hedged
// `said` critical, a `pointed` critical, and a `walked` important.
const (
	hedgedBody  = "This might be a race because the guard could be skipped."
	pointedBody = "The parser drops the error and returns a zero value."
	walkedBody  = "The retry loop resets the counter on every pass."

	hedgedSig  = "411fae6d1b4167cc3947b7bf235a680b33ed022f"
	pointedSig = "56c635390ebe6f0001d215a48daa776c51698152"
	walkedSig  = "75f03caf240c3b22133f55e87ae3fca76f2e8847"

	// The validator's stored prose. It must never reach a posted body.
	validatorReason = "VALIDATOR-PRIVATE: the body hedges with 'might' and names no evidence in the cited hunk."
)

// validationReview is the three-candidate review.json every case posts.
func validationReview() string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Three findings the validator judges.","approved":false,"comments":[` +
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"` + hedgedBody + `","rigor":"said","severity":"critical"},` +
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"` + pointedBody + `","rigor":"pointed","severity":"critical"},` +
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"` + walkedBody + `","rigor":"walked","severity":"important"}]}`
}

// rejectedFixture is one validation.json rejecting the hedged critical. Its
// hash is pinned so the receipt proves the exact bytes it applied.
const rejectedFixture = `{"schema_version":"1","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","results":[{"sig":"411fae6d1b4167cc3947b7bf235a680b33ed022f","verdict":"REJECTED","reason":"VALIDATOR-PRIVATE: the body hedges with 'might' and names no evidence in the cited hunk."}]}`

// rejectedFixtureSHA is the SHA-256 of rejectedFixture.
const rejectedFixtureSHA = "da80ca637d8ecc3f33a986c954c3f38f47cc26cea16ca25e63ed5eb930cf93b9"

// baseConfig writes the trusted-base typed configuration before the controls
// snapshot is taken; rewriteConfig simulates a head edit afterwards.
func baseConfig(t *testing.T, body string) {
	t.Helper()
	writeFile(t, ".verdict/config.json", body)
}

// validationBundle writes the provider bundle post merges, optionally with a
// trusted-base configuration, and returns nothing: every path is relative to
// the chdir'd sandbox root the shared helpers use.
func validationBundle(t *testing.T, config string) {
	t.Helper()
	validationBundleWithReview(t, config, validationReview())
}

// validationBundleWithReview writes the bundle over one review document.
func validationBundleWithReview(t *testing.T, config, review string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/7"
	writeFile(t, dir+"/PR.json", `{"state":"OPEN","headRefOid":"`+coverageHead+
		`","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"someone-else"}}`)
	writeFile(t, dir+"/full.diff", coverageDiff)
	writeFile(t, dir+"/review.json", review)
	if config != "" {
		baseConfig(t, config)
	}
	seedControls(t, dir)
}

// writeValidation plants the artifact at the bundle root.
func writeValidation(t *testing.T, body string) {
	t.Helper()
	writeFile(t, ".verdict/state/7/validation.json", body)
}

// validationCommander answers the gh calls a post run makes and keeps every
// posted payload, so a case grades what actually reached the host.
type validationCommander struct {
	t        *testing.T
	payloads []string
}

func (c *validationCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Helper()
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "--method POST") && strings.Contains(argv, "/reviews"):
		raw, _ := io.ReadAll(cmd.Stdin)
		c.payloads = append(c.payloads, string(raw))
		return core.Result{Stdout: `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101",` +
			`"submitted_at":"2026-01-01T00:00:00Z"}`}, nil
	case strings.Contains(argv, "--paginate") && (strings.Contains(argv, "/reviews") || strings.Contains(argv, "/comments")):
		return core.Result{Stdout: "[]"}, nil
	case strings.Contains(argv, "/reviews"):
		return core.Result{Stdout: `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101",` +
			`"submitted_at":"2026-01-01T00:00:00Z"}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	case strings.Contains(argv, ".head.sha"):
		return core.Result{Stdout: coverageHead + "\n"}, nil
	case strings.Contains(argv, "user"):
		return core.Result{Stdout: "someone-else\n"}, nil
	default:
		c.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{ExitCode: 1}, errors.New("unexpected argv")
	}
}

// runValidationPost posts the fixture through the capturing commander.
func runValidationPost(t *testing.T, flags core.PostFlags) (*core.PostResponse, *core.RefusalError, *validationCommander) {
	t.Helper()
	resp, ref, cmdr, _ := runValidationPostProgress(t, flags)
	return resp, ref, cmdr
}

// runValidationPostProgress also returns the run's warnings.
func runValidationPostProgress(t *testing.T, flags core.PostFlags) (*core.PostResponse, *core.RefusalError, *validationCommander, []string) {
	t.Helper()
	cmdr := &validationCommander{t: t}
	progress := &collector{}
	root := ""
	req := &core.PostRequest{
		Runtime:   &core.Runtime{Env: map[string]string{}, Now: testRuntime(progress).Now, Progress: progress},
		Repo:      "acme/app",
		Pr:        "7",
		Root:      &root,
		Flags:     flags,
		Effective: core.Defaults(),
		JSONMode:  true,
	}
	req.Runtime.Commander = cmdr
	resp, ref := postcore.Run(context.Background(), req)
	return resp, ref, cmdr, progress.warns
}

// validationFlags is the ordinary non-binding post the cases run.
func validationFlags() core.PostFlags {
	return core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}
}

// decodeEntries reads the stored findings array.
func decodeEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, raw)
	}
	return out
}

// findingEntry returns one stored finding by sig.
func findingEntry(t *testing.T, sig string) map[string]any {
	t.Helper()
	for _, entry := range decodeEntries(t, ".verdict/state/7/findings.json") {
		if entry["sig"] == sig {
			return entry
		}
	}
	t.Fatalf("no finding stored for sig %s", sig)
	return nil
}

// TestPostRejectedAdvisory pins criteria 1 and 7: advisory mode posts the
// rejected finding unchanged, warns with the validator's reason, and counts
// it in post.json beside the exact-file hash.
func TestPostRejectedAdvisory(t *testing.T) {
	validationBundle(t, "")
	writeValidation(t, rejectedFixture)
	_, ref, cmdr := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("advisory post refused: %v (%s)", ref, ref.Message)
	}
	receipt := decodeObject(t, ".verdict/state/7/post.json")
	counts, _ := receipt["validation_counts"].(map[string]any)
	if counts["rejected"] != 1.0 || counts["unvalidated"] != 2.0 {
		t.Fatalf("validation_counts = %v, want 1 rejected and 2 unvalidated", receipt["validation_counts"])
	}
	if receipt["validation_hash"] != rejectedFixtureSHA {
		t.Fatalf("validation_hash = %v, want the file's SHA-256", receipt["validation_hash"])
	}
	if receipt["validation_mode"] != "warn" {
		t.Fatalf("validation_mode = %v, want warn", receipt["validation_mode"])
	}
	stored := findingEntry(t, hedgedSig)
	if stored["status"] != "open" || stored["withheld"] != nil {
		t.Fatalf("advisory mode stored the hedged finding as %v", stored)
	}
	if !strings.Contains(strings.Join(cmdr.payloads, "\n"), hedgedBody) {
		t.Fatal("advisory mode withheld the finding from the payload")
	}
}

// TestPostRejectedWithheldInGate pins criterion 2: gate mode stamps the D2
// receipt, keeps the finding open, and omits it from the payload.
func TestPostRejectedWithheldInGate(t *testing.T) {
	validationBundle(t, `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`)
	writeValidation(t, rejectedFixture)
	_, ref, cmdr := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("gate post refused: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, hedgedSig)
	if stored["status"] != "open" {
		t.Fatalf("withheld finding status = %v, want open", stored["status"])
	}
	withheld, _ := stored["withheld"].(map[string]any)
	if withheld["source"] != "validation" || withheld["reason"] != validatorReason {
		t.Fatalf("withheld = %v, want the validation source and the validator's reason", stored["withheld"])
	}
	if strings.Contains(strings.Join(cmdr.payloads, "\n"), hedgedBody) {
		t.Fatal("gate mode posted the withheld finding")
	}
	if !strings.Contains(strings.Join(cmdr.payloads, "\n"), walkedBody) {
		t.Fatal("gate mode withheld a confirmed finding too")
	}
}

// TestDowngradeApplied pins criterion 3: one rank down, a history event, and
// the downgraded count.
func TestDowngradeApplied(t *testing.T) {
	validationBundle(t, "")
	writeValidation(t, `{"schema_version":"1","head_sha":"`+coverageHead+`","results":[{"sig":"`+pointedSig+
		`","verdict":"DOWNGRADED","reason":"Real bug, local blast radius; important, not critical.",`+
		`"from_severity":"critical","to_severity":"important"}]}`)
	_, ref, _ := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("post refused: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, pointedSig)
	if stored["severity"] != "important" {
		t.Fatalf("severity = %v, want important", stored["severity"])
	}
	history, _ := stored["history"].([]any)
	last, _ := history[len(history)-1].(map[string]any)
	if last["event"] != "validation" || last["actor"] != "validator" {
		t.Fatalf("history tail = %v, want a validation event by the validator", last)
	}
	receipt := decodeObject(t, ".verdict/state/7/post.json")
	if counts, _ := receipt["validation_counts"].(map[string]any); counts["downgraded"] != 1.0 {
		t.Fatalf("validation_counts = %v, want 1 downgraded", receipt["validation_counts"])
	}
}

// TestNoValidationReceipt pins criterion 7's absent half: a run with no pass
// records a null hash and counts every finding unvalidated.
func TestNoValidationReceipt(t *testing.T) {
	bundle(t, "local-rr", reviewJSON(""))
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-rr")); ref != nil {
		t.Fatalf("record refused: %v", ref)
	}
	receipt := decodeObject(t, ".verdict/state/local-rr/record.json")
	if hash, present := receipt["validation_hash"]; !present || hash != nil {
		t.Fatalf("validation_hash = %v (present %v), want null", hash, present)
	}
	counts, _ := receipt["validation_counts"].(map[string]any)
	if counts["unvalidated"] != 1.0 {
		t.Fatalf("validation_counts = %v, want 1 unvalidated", receipt["validation_counts"])
	}
	floors, _ := receipt["rigor_floors"].(map[string]any)
	if floors["critical"] != "walked" || floors["important"] != "pointed" || floors["informational"] != "said" {
		t.Fatalf("rigor_floors = %v, want the built-in floors", receipt["rigor_floors"])
	}
}

// TestRigorFloorGateAndForce pins criterion 4: a said critical below the
// walked floor refuses, and --force posts with the reason and sigs recorded.
func TestRigorFloorGateAndForce(t *testing.T) {
	validationBundle(t, "")
	flags := validationFlags()
	flags.RequireRigor = []string{"critical=walked"}
	_, ref, _ := runValidationPost(t, flags)
	if ref == nil || ref.Code != "rigor-below-floor" {
		t.Fatalf("gate refusal = %v, want rigor-below-floor", ref)
	}
	if !strings.Contains(ref.Message, "411fae6 (critical said < walked)") {
		t.Fatalf("refusal = %q, want it to name the sig and the pair", ref.Message)
	}
	flags.Force = true
	if _, ref, _ := runValidationPost(t, flags); ref != nil {
		t.Fatalf("forced post refused: %v (%s)", ref, ref.Message)
	}
	receipt := decodeObject(t, ".verdict/state/7/post.json")
	forced, _ := receipt["forced"].([]any)
	if len(forced) != 1 {
		t.Fatalf("forced = %v, want one recorded bypass", receipt["forced"])
	}
	entry, _ := forced[0].(map[string]any)
	sigs, _ := entry["sigs"].([]any)
	if entry["reason"] != "rigor-below-floor" || len(sigs) != 1 || sigs[0] != "411fae6" {
		t.Fatalf("forced[0] = %v, want the rigor-below-floor reason and the offending sig", entry)
	}
}

// TestCriticalDowngradesInsteadOfRefusing pins criterion 5: a pointed
// critical clears the important floor, so the gate downgrades it rather than
// refusing the post.
func TestCriticalDowngradesInsteadOfRefusing(t *testing.T) {
	validationBundleWithReview(t, "", `{"schema_version":"1","body":"> [!IMPORTANT]\n> One pointed critical.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+pointedBody+`","rigor":"pointed","severity":"critical"}]}`)
	flags := validationFlags()
	flags.RequireRigor = []string{"critical=walked"}
	_, ref, _ := runValidationPost(t, flags)
	if ref != nil {
		t.Fatalf("post refused instead of downgrading: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, pointedSig)
	if stored["severity"] != "important" {
		t.Fatalf("severity = %v, want important", stored["severity"])
	}
	history, _ := stored["history"].([]any)
	last, _ := history[len(history)-1].(map[string]any)
	if last["event"] != "validation" {
		t.Fatalf("history tail = %v, want the floor downgrade recorded", last)
	}
}

// TestPolicyFromBaseBeatsHeadEdit pins criterion 8: the gate reads the
// trusted-base snapshot, so rewriting the head's configuration cannot move it.
func TestPolicyFromBaseBeatsHeadEdit(t *testing.T) {
	validationBundle(t, `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`)
	writeValidation(t, rejectedFixture)
	baseConfig(t, `{"schema_version":"1","rigor_policy":{"mode":"warn"}}`)
	_, ref, _ := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("post refused: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, hedgedSig)
	withheld, _ := stored["withheld"].(map[string]any)
	if withheld["source"] != "validation" {
		t.Fatalf("head edit moved the gate: withheld = %v", stored["withheld"])
	}
}

// TestValidatorReasonNeverPosted pins criterion 10: the reason is read (it
// reaches the warnings and the stored receipt) and never rendered into a byte
// that reaches the host, in either mode. The second half is structural rather
// than guarded: the validator's prose is written only to the warnings and the
// withheld receipt, and neither is rendered into the published body.
func TestValidatorReasonNeverPosted(t *testing.T) {
	cases := []struct{ name, config string }{
		{"advisory", ""},
		{"gate", `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validationBundle(t, tc.config)
			writeValidation(t, rejectedFixture)
			_, ref, cmdr, warns := runValidationPostProgress(t, validationFlags())
			if ref != nil {
				t.Fatalf("post refused: %v (%s)", ref, ref.Message)
			}
			for _, payload := range cmdr.payloads {
				if strings.Contains(payload, "VALIDATOR-PRIVATE") {
					t.Fatalf("posted payload quotes the validator's reason: %s", payload)
				}
			}
			stored := findingEntry(t, hedgedSig)
			if tc.config == "" {
				if stored["withheld"] != nil {
					t.Fatalf("advisory mode stored a withheld receipt: %v", stored["withheld"])
				}
				if !strings.Contains(strings.Join(cmdr.payloads, "\n"), hedgedBody) {
					t.Fatal("advisory mode withheld the judged finding")
				}
				if !strings.Contains(strings.Join(warns, "\n"), "VALIDATOR-PRIVATE") {
					t.Fatalf("the advisory warning dropped the validator's reason: %v", warns)
				}
				return
			}
			if !strings.Contains(jq.Compact(stored["withheld"]), "VALIDATOR-PRIVATE") {
				t.Fatal("the stored receipt lost the validator's reason")
			}
		})
	}
}
