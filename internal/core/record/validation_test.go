package recordcore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// movedBody is an out-of-hunk claim the merge moves into the body section.
const (
	movedBody = "Out-of-hunk claim the validator rejects."
	movedSig  = "ea74dc2017832acaf11b2413f27a2dce9f562e19"
)

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

func validationBundle(t *testing.T, config string) {
	t.Helper()
	validationBundleWithReview(t, config, validationReview())
}

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

func writeValidation(t *testing.T, body string) {
	t.Helper()
	writeFile(t, ".verdict/state/7/validation.json", body)
}

// validationCommander keeps every posted payload, so a case grades what
// reached the host.
type validationCommander struct {
	t        *testing.T
	payloads []string
}

func (c *validationCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Helper()
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "--method POST") && strings.Contains(argv, "/reviews"):
		raw, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			c.t.Fatalf("read posted payload: %v", err)
		}
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

func runValidationPost(t *testing.T, flags postcore.Flags, floors ...string) (*postcore.Response, *core.RefusalError, *validationCommander) {
	t.Helper()
	resp, ref, cmdr, _ := runValidationPostProgress(t, flags, floors...)
	return resp, ref, cmdr
}

func runValidationPostProgress(t *testing.T, flags postcore.Flags, floors ...string) (*postcore.Response, *core.RefusalError, *validationCommander, []string) {
	t.Helper()
	cmdr := &validationCommander{t: t}
	progress := &collector{}
	root := ""
	req := &postcore.Request{
		Runtime:      &core.Runtime{Env: map[string]string{}, Now: testRuntime(progress).Now, Progress: progress},
		Repo:         "acme/app",
		Pr:           "7",
		Root:         &root,
		Flags:        flags,
		RequireRigor: floors,
		Effective:    core.Defaults(),
		JSONMode:     true,
	}
	req.Commander = cmdr
	resp, ref := postcore.Run(context.Background(), req)
	return resp, ref, cmdr, progress.warns
}

func validationFlags() postcore.Flags {
	return postcore.Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}
}

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

// objectAt fails the test when the stored shape is not the object the receipt
// promises.
func objectAt(t *testing.T, v any, key string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want an object", key, v)
	}
	return m
}

func listAt(t *testing.T, v any, key string) []any {
	t.Helper()
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list", key, v)
	}
	return items
}

func receiptAt(t *testing.T, receipt map[string]any, key string) map[string]any {
	t.Helper()
	return objectAt(t, receipt[key], key)
}

func historyTail(t *testing.T, stored map[string]any) map[string]any {
	t.Helper()
	history := listAt(t, stored["history"], "history")
	return objectAt(t, history[len(history)-1], "history tail")
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
	assertAdvisoryReceipt(t, decodeObject(t, ".verdict/state/7/post.json"))
	stored := findingEntry(t, hedgedSig)
	if stored["status"] != "open" || stored["withheld"] != nil {
		t.Fatalf("advisory mode stored the hedged finding as %v", stored)
	}
	if !strings.Contains(strings.Join(cmdr.payloads, "\n"), hedgedBody) {
		t.Fatal("advisory mode withheld the finding from the payload")
	}
}

func assertAdvisoryReceipt(t *testing.T, receipt map[string]any) {
	t.Helper()
	counts := receiptAt(t, receipt, "validation_counts")
	if counts["rejected"] != 1.0 || counts["unvalidated"] != 2.0 {
		t.Fatalf("validation_counts = %v, want 1 rejected and 2 unvalidated", receipt["validation_counts"])
	}
	if receipt["validation_hash"] != rejectedFixtureSHA {
		t.Fatalf("validation_hash = %v, want the file's SHA-256", receipt["validation_hash"])
	}
	if receipt["validation_mode"] != "warn" {
		t.Fatalf("validation_mode = %v, want warn", receipt["validation_mode"])
	}
}

// TestRejectedMovedFindingStaysOutOfBody pins criterion 2 for a finding the
// merge moved out of the diff: the pass stamps the entry before the moved
// section is curated from it, so the same hide path drops it from the body.
func TestRejectedMovedFindingStaysOutOfBody(t *testing.T) {
	validationBundleWithReview(t, `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`,
		`{"schema_version":"1","body":"> [!IMPORTANT]\n> One finding outside the diff.","approved":false,"comments":[`+
			`{"path":"notes.txt","line":99,"side":"RIGHT","body":"`+movedBody+`","rigor":"walked","severity":"important"}]}`)
	writeValidation(t, `{"schema_version":"1","head_sha":"`+coverageHead+`","results":[{"sig":"`+movedSig+
		`","verdict":"REJECTED","reason":"The cited line is outside the diff, so no hunk can carry it."}]}`)
	_, ref, cmdr := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("gate post refused: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, movedSig)
	if stored["status"] != "open" {
		t.Fatalf("withheld finding status = %v, want open", stored["status"])
	}
	if withheld := receiptAt(t, stored, "withheld"); withheld["source"] != "validation" {
		t.Fatalf("withheld = %v, want the validation source", stored["withheld"])
	}
	if strings.Contains(strings.Join(cmdr.payloads, "\n"), movedBody) {
		t.Fatal("the moved finding reached the posted body")
	}
}

// TestDowngradeRefusesMismatchedFrom pins the never-upgrade rule: a downgrade
// names the severity the finding carries, so a stale pair cannot raise it.
func TestDowngradeRefusesMismatchedFrom(t *testing.T) {
	validationBundleWithReview(t, "", `{"schema_version":"1","body":"> [!IMPORTANT]\n> One low finding.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+walkedBody+`","rigor":"walked","severity":"informational"}]}`)
	writeValidation(t, `{"schema_version":"1","head_sha":"`+coverageHead+`","results":[{"sig":"`+walkedSig+
		`","verdict":"DOWNGRADED","reason":"Real bug, local blast radius.","from_severity":"critical","to_severity":"important"}]}`)
	_, ref, _ := runValidationPost(t, validationFlags())
	if ref == nil || ref.Code != "bad-validation" {
		t.Fatalf("refusal = %v, want bad-validation", ref)
	}
	if !strings.Contains(ref.Message, "from_severity critical") {
		t.Fatalf("refusal = %q, want it to name the pair the finding does not carry", ref.Message)
	}
}

// TestCarriedOverFindingValidated pins sig resolution against the merged store:
// a finding carried from the prior findings.json is a candidate like any other.
func TestCarriedOverFindingValidated(t *testing.T) {
	validationBundleWithReview(t, `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`,
		`{"schema_version":"1","body":"> [!IMPORTANT]\n> One finding the next review drops.","approved":false,"comments":[`+
			`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+walkedBody+`","rigor":"walked","severity":"important"}]}`)
	if _, ref, _ := runValidationPost(t, validationFlags()); ref != nil {
		t.Fatalf("first post refused: %v (%s)", ref, ref.Message)
	}
	writeFile(t, ".verdict/state/7/review.json", `{"schema_version":"1","body":"> [!IMPORTANT]\n> A later review.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":3,"side":"RIGHT","body":"A later claim with its own anchor.","rigor":"walked","severity":"important"}]}`)
	writeValidation(t, `{"schema_version":"1","head_sha":"`+coverageHead+`","results":[{"sig":"`+walkedSig+
		`","verdict":"REJECTED","reason":"The carried claim cites no evidence in the hunk it names."}]}`)
	_, ref, cmdr := runValidationPost(t, validationFlags())
	if ref != nil {
		t.Fatalf("carried finding refused: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, walkedSig)
	if withheld := receiptAt(t, stored, "withheld"); withheld["source"] != "validation" {
		t.Fatalf("withheld = %v, want the validation source on the carried finding", stored["withheld"])
	}
	if strings.Join(cmdr.payloads, "\n") == "" {
		t.Fatal("the second post sent no payload")
	}
}

// TestWithheldFindingNotUnvalidated pins the tally: a finding the floor already
// withheld is not a candidate, so no pass owes it a result.
func TestWithheldFindingNotUnvalidated(t *testing.T) {
	validationBundleWithReview(t, "", `{"schema_version":"1","body":"> [!IMPORTANT]\n> One posted finding.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+walkedBody+`","rigor":"walked","severity":"important"},`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+pointedBody+`","rigor":"pointed","severity":"informational"}]}`)
	writeValidation(t, `{"schema_version":"1","head_sha":"`+coverageHead+`","results":[{"sig":"`+walkedSig+
		`","verdict":"CONFIRMED","reason":"The cited hunk resets the counter on every pass."}]}`)
	flags := validationFlags()
	flags.MinSeverity = "important"
	if _, ref, _ := runValidationPost(t, flags); ref != nil {
		t.Fatalf("post refused: %v (%s)", ref, ref.Message)
	}
	counts := receiptAt(t, decodeObject(t, ".verdict/state/7/post.json"), "validation_counts")
	if counts["unvalidated"] != 0.0 || counts["confirmed"] != 1.0 {
		t.Fatalf("validation_counts = %v, want the withheld finding outside the tally", counts)
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
	withheld := receiptAt(t, stored, "withheld")
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
	if last := historyTail(t, stored); last["event"] != "validation" || last["actor"] != "validator" {
		t.Fatalf("history tail = %v, want a validation event by the validator", last)
	}
	receipt := decodeObject(t, ".verdict/state/7/post.json")
	if counts := receiptAt(t, receipt, "validation_counts"); counts["downgraded"] != 1.0 {
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
	counts := receiptAt(t, receipt, "validation_counts")
	if counts["unvalidated"] != 1.0 {
		t.Fatalf("validation_counts = %v, want 1 unvalidated", receipt["validation_counts"])
	}
	floors := receiptAt(t, receipt, "rigor_floors")
	if floors["critical"] != "walked" || floors["important"] != "pointed" || floors["informational"] != "said" {
		t.Fatalf("rigor_floors = %v, want the built-in floors", receipt["rigor_floors"])
	}
}

// TestRigorFloorGateAndForce pins criterion 4: a said critical below the
// walked floor refuses, and --force posts with the reason and sigs recorded.
func TestRigorFloorGateAndForce(t *testing.T) {
	validationBundle(t, "")
	_, ref, _ := runValidationPost(t, validationFlags(), "critical=walked")
	if ref == nil || ref.Code != "rigor-below-floor" {
		t.Fatalf("gate refusal = %v, want rigor-below-floor", ref)
	}
	if !strings.Contains(ref.Message, "411fae6 (critical said < walked)") {
		t.Fatalf("refusal = %q, want it to name the sig and the pair", ref.Message)
	}
	bypass := validationFlags()
	bypass.Force = true
	if _, ref, _ := runValidationPost(t, bypass, "critical=walked"); ref != nil {
		t.Fatalf("forced post refused: %v (%s)", ref, ref.Message)
	}
	receipt := decodeObject(t, ".verdict/state/7/post.json")
	forced := listAt(t, receipt["forced"], "forced")
	if len(forced) != 1 {
		t.Fatalf("forced = %v, want one recorded bypass", receipt["forced"])
	}
	entry := objectAt(t, forced[0], "forced[0]")
	sigs := listAt(t, entry["sigs"], "forced[0].sigs")
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
	_, ref, _ := runValidationPost(t, validationFlags(), "critical=walked")
	if ref != nil {
		t.Fatalf("post refused instead of downgrading: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, pointedSig)
	if stored["severity"] != "important" {
		t.Fatalf("severity = %v, want important", stored["severity"])
	}
	if last := historyTail(t, stored); last["event"] != "validation" {
		t.Fatalf("history tail = %v, want the floor downgrade recorded", last)
	}
}

// writtenReview is the closed-finding fixture: one `said` critical the run can
// close, plus a clean important finding so the post still has something to say.
func writtenReview(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, `{"schema_version":"1","body":"> [!IMPORTANT]\n> Two findings.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+hedgedBody+`","rigor":"said","severity":"critical"},`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+walkedBody+`","rigor":"walked","severity":"important"}]}`)
}

// verifyClaim marks one finding addressed in the same run that posts it.
func verifyClaim(t *testing.T, sig string) {
	t.Helper()
	writeFile(t, ".verdict/state/7/verification.json", `[{"sig":"`+sig+
		`","verdict":"VERIFIED","resultHead":"`+coverageHead+`","verifyCmd":"go test ./..."}]`)
}

// TestFloorsSkipSameRunAddressedFinding pins the closed-finding rule for the
// run that closes it: triage and verification settle the status before the
// floors judge, so an addressed critical is neither refused nor downgraded.
func TestFloorsSkipSameRunAddressedFinding(t *testing.T) {
	validationBundle(t, "")
	writtenReview(t, ".verdict/state/7/review.json")
	verifyClaim(t, hedgedSig)
	_, ref, _ := runValidationPost(t, validationFlags(), "critical=walked", "important=said")
	if ref != nil {
		t.Fatalf("post refused on a finding this run addressed: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, hedgedSig)
	if stored["status"] != "addressed" {
		t.Fatalf("status = %v, want addressed", stored["status"])
	}
	if stored["severity"] != "critical" {
		t.Fatalf("severity = %v, want the closed finding left alone", stored["severity"])
	}
}

// TestFloorsSkipStoredAddressedFinding pins the same rule for a finding a prior
// run closed: a closed candidate is not an open finding under a floor.
func TestFloorsSkipStoredAddressedFinding(t *testing.T) {
	validationBundle(t, "")
	writtenReview(t, ".verdict/state/7/review.json")
	verifyClaim(t, hedgedSig)
	if _, ref, _ := runValidationPost(t, validationFlags()); ref != nil {
		t.Fatalf("first post refused: %v (%s)", ref, ref.Message)
	}
	// The next run reports only the clean finding, so the closed one carries
	// over as stored history rather than reopening.
	writeFile(t, ".verdict/state/7/review.json", `{"schema_version":"1","body":"> [!IMPORTANT]\n> One finding.","approved":false,"comments":[`+
		`{"path":"notes.txt","line":2,"side":"RIGHT","body":"`+walkedBody+`","rigor":"walked","severity":"important"}]}`)
	os.Remove(".verdict/state/7/verification.json")
	if _, ref, _ := runValidationPost(t, validationFlags(), "critical=walked"); ref != nil {
		t.Fatalf("post refused on a stored addressed finding: %v (%s)", ref, ref.Message)
	}
	stored := findingEntry(t, hedgedSig)
	if stored["status"] != "addressed" || stored["severity"] != "critical" {
		t.Fatalf("stored finding = %v/%v, want addressed/critical", stored["status"], stored["severity"])
	}
}

// TestFloorsStillRefuseOpenFinding pins that the closed-finding rule did not
// disarm the gate: an open `said` critical still refuses.
func TestFloorsStillRefuseOpenFinding(t *testing.T) {
	validationBundle(t, "")
	writtenReview(t, ".verdict/state/7/review.json")
	_, ref, _ := runValidationPost(t, validationFlags(), "critical=walked")
	if ref == nil || ref.Code != "rigor-below-floor" {
		t.Fatalf("refusal = %v, want rigor-below-floor", ref)
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
	if withheld := receiptAt(t, stored, "withheld"); withheld["source"] != "validation" {
		t.Fatalf("head edit moved the gate: withheld = %v", stored["withheld"])
	}
}

// TestValidatorReasonNeverPosted pins criterion 10: the reason is read (it
// reaches the warnings and the stored receipt) and never rendered into a byte
// that reaches the host, in either mode. The second half is structural rather
// than guarded: the validator's prose is written only to the warnings and the
// withheld receipt, and neither is rendered into the published body.
func TestValidatorReasonNeverPosted(t *testing.T) {
	for _, tc := range []struct {
		check  func(*testing.T, []string, *validationCommander)
		name   string
		config string
	}{
		{check: wroteAdvisoryReason, name: "advisory"},
		{check: wroteWithheldReason, name: "gate", config: `{"schema_version":"1","rigor_policy":{"mode":"gate"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validationBundle(t, tc.config)
			writeValidation(t, rejectedFixture)
			_, ref, cmdr, warns := runValidationPostProgress(t, validationFlags())
			if ref != nil {
				t.Fatalf("post refused: %v (%s)", ref, ref.Message)
			}
			noPostedReason(t, cmdr)
			tc.check(t, warns, cmdr)
		})
	}
}

func noPostedReason(t *testing.T, cmdr *validationCommander) {
	t.Helper()
	for _, payload := range cmdr.payloads {
		if strings.Contains(payload, "VALIDATOR-PRIVATE") {
			t.Fatalf("posted payload quotes the validator's reason: %s", payload)
		}
	}
}

func wroteAdvisoryReason(t *testing.T, warns []string, cmdr *validationCommander) {
	t.Helper()
	if stored := findingEntry(t, hedgedSig); stored["withheld"] != nil {
		t.Fatalf("advisory mode stored a withheld receipt: %v", stored["withheld"])
	}
	if !strings.Contains(strings.Join(cmdr.payloads, "\n"), hedgedBody) {
		t.Fatal("advisory mode withheld the judged finding")
	}
	if !strings.Contains(strings.Join(warns, "\n"), "VALIDATOR-PRIVATE") {
		t.Fatalf("the advisory warning dropped the validator's reason: %v", warns)
	}
}

func wroteWithheldReason(t *testing.T, _ []string, _ *validationCommander) {
	t.Helper()
	stored := findingEntry(t, hedgedSig)
	if !strings.Contains(jq.Compact(stored["withheld"]), "VALIDATOR-PRIVATE") {
		t.Fatal("the stored receipt lost the validator's reason")
	}
}

func emptyPass(head, validator string) string {
	return `{"schema_version":"1","head_sha":"` + head + `",` + validator + `"results":[]}`
}

func shippedValidatePromptSHA(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "procedure", "prompts", "validate.md"))
	if err != nil {
		t.Fatalf("read the shipped validate prompt: %v", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func promptWarnings(warns []string) []string {
	var out []string
	for _, w := range warns {
		if strings.Contains(w, "prompts/validate.md") {
			out = append(out, w)
		}
	}
	return out
}

// TestPostWarnsWhenValidatorPromptDiffers pins the drift warning: a pass that
// recorded another prompt digest still applies, and post names both digests.
func TestPostWarnsWhenValidatorPromptDiffers(t *testing.T) {
	shipped := shippedValidatePromptSHA(t)[:7]
	validationBundle(t, "")
	writeValidation(t, emptyPass(coverageHead, `"validator":{"prompt_sha256":"`+strings.Repeat("ab", 32)+`"},`))
	_, ref, _, warns := runValidationPostProgress(t, validationFlags())
	if ref != nil {
		t.Fatalf("post refused: %v (%s)", ref, ref.Message)
	}
	got := promptWarnings(warns)
	want := "validation.json was produced with prompts/validate.md sha256 abababa, but this build ships " + shipped +
		": the pass may have judged against a different prompt"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("prompt warnings = %q, want exactly %q", got, want)
	}
}

// TestPostStaysQuietWhenValidatorPromptMatches pins the other side: the
// shipped digest, in either case, and an absent digest raise no warning.
func TestPostStaysQuietWhenValidatorPromptMatches(t *testing.T) {
	for name, validator := range map[string]string{
		"shipped digest": `"validator":{"prompt_sha256":"` + shippedValidatePromptSHA(t) + `"},`,
		"upper case":     `"validator":{"prompt_sha256":"` + strings.ToUpper(shippedValidatePromptSHA(t)) + `"},`,
		"no digest":      `"validator":{"model":"m"},`,
		"no validator":   "",
	} {
		t.Run(name, func(t *testing.T) {
			assertPostPromptQuiet(t, validator)
		})
	}
}

func assertPostPromptQuiet(t *testing.T, validator string) {
	t.Helper()
	validationBundle(t, "")
	writeValidation(t, emptyPass(coverageHead, validator))
	_, ref, _, warns := runValidationPostProgress(t, validationFlags())
	if ref != nil {
		t.Fatalf("post refused: %v (%s)", ref, ref.Message)
	}
	if got := promptWarnings(warns); len(got) != 0 {
		t.Fatalf("prompt warnings = %q, want none", got)
	}
}

// TestRecordWarnsWhenValidatorPromptDiffers pins the same warning on record,
// which shares the merge with post.
func TestRecordWarnsWhenValidatorPromptDiffers(t *testing.T) {
	bundle(t, "local-drift", reviewJSON(""))
	writeFile(t, ".verdict/state/local-drift/validation.json",
		emptyPass(strings.Repeat("a", 40), `"validator":{"prompt_sha256":"`+strings.Repeat("cd", 32)+`"},`))
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-drift")); ref != nil {
		t.Fatalf("record refused: %v (%s)", ref, ref.Message)
	}
	got := promptWarnings(c.warns)
	if len(got) != 1 || !strings.HasPrefix(got[0], "validation.json was produced with prompts/validate.md sha256 cdcdcdc, but this build ships ") {
		t.Fatalf("prompt warnings = %q, want one naming the recorded digest cdcdcdc", got)
	}
}
