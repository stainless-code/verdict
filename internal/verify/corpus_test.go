package verify_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

// corpusGrade mirrors the eval's grade_refuse: typed refusal at exit 1,
// one-doc envelope with the exact code, try: hint plus quoted fragment,
// patch.json byte-identical, no .verify.out write.
func corpusGradeRefuse(t *testing.T, dir, pr, want string, before []byte) {
	t.Helper()
	out := harness.Run(t, true, dir, t.TempDir(), map[string]string{"AWS_KEY": "fuzzcanary"}, "verify", "acme/app", pr, "--json", "--timeout", "10")
	gradeRefuseStatus(t, pr, out)
	env := parseRefuseEnvelope(t, pr, out.Stdout)
	gradeRefuseEnvelope(t, pr, env, want)
	gradeRefuseStore(t, dir, pr, before)
}

// gradeRefuseStatus asserts the typed-refusal exit code.
func gradeRefuseStatus(t *testing.T, pr string, out parity.Outputs) {
	t.Helper()
	if out.Status != 1 {
		t.Errorf("%s: exit %d, want 1\nstdout: %s\nstderr: %s", pr, out.Status, out.Stdout, out.Stderr)
	}
}

// refuseEnvelope is the one-doc typed-refusal envelope shape.
type refuseEnvelope struct {
	Data  *struct{} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseRefuseEnvelope decodes the refusal envelope.
func parseRefuseEnvelope(t *testing.T, pr, stdout string) refuseEnvelope {
	t.Helper()
	var env refuseEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("%s: envelope unparseable: %v\n%s", pr, err, stdout)
	}
	return env
}

// gradeRefuseEnvelope asserts the typed code, null data, and the hint
// plus quoted fragment in the message.
func gradeRefuseEnvelope(t *testing.T, pr string, env refuseEnvelope, want string) {
	t.Helper()
	if env.Error == nil || env.Error.Code != want {
		t.Errorf("%s: code %v, want %s", pr, env.Error, want)
	}
	if env.Data != nil {
		t.Errorf("%s: data not null", pr)
	}
	if env.Error != nil && (!strings.Contains(env.Error.Message, "try:") || !strings.Contains(env.Error.Message, "'")) {
		t.Errorf("%s: message misses try: hint or quoted fragment", pr)
	}
}

// gradeRefuseStore asserts the patch record is byte-identical and no
// proof file was written.
func gradeRefuseStore(t *testing.T, dir, pr string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(filepath.Join(dir, ".review", pr, "patch.json"))
	if err != nil {
		t.Errorf("%s: patch.json unreadable: %v", pr, err)
		return
	}
	if !bytes.Equal(after, before) {
		t.Errorf("%s: patch.json mutated", pr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".review", pr, ".verify.out")); !os.IsNotExist(err) {
		t.Errorf("%s: .verify.out written on refusal", pr)
	}
}

// corpusGradeExec mirrors the eval's grade_exec: error null, numeric
// data.exit, verifyExit recorded, .verify.out written.
func corpusGradeExec(t *testing.T, dir, pr string) {
	t.Helper()
	home := t.TempDir()
	out := harness.Run(t, true, dir, home, map[string]string{"AWS_KEY": "fuzzcanary"}, "verify", "acme/app", pr, "--json", "--timeout", "10")
	var env struct {
		Data *struct {
			Exit *float64 `json:"exit"`
		} `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &env); err != nil {
		t.Errorf("%s: envelope unparseable: %v\n%s", pr, err, out.Stdout)
		return
	}
	if env.Error != nil || env.Data == nil || env.Data.Exit == nil {
		t.Errorf("%s: refused or broke: exit %d code %v\n%s", pr, out.Status, env.Error, out.Stdout)
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".review", pr, "patch.json"))
	if err != nil {
		t.Errorf("%s: patch.json unreadable: %v", pr, err)
		return
	}
	var patch map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil || patch["verifyExit"] == nil {
		t.Errorf("%s: verifyExit not recorded", pr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".review", pr, ".verify.out")); err != nil {
		t.Errorf("%s: .verify.out missing", pr)
	}
}

// writeCorpusFixtures crafts the sandbox and /tmp fixtures the executed
// corpus rows need (mirroring the eval case).
func writeCorpusFixtures(t *testing.T, dir string) {
	t.Helper()
	// Sandbox fixtures the executed rows need (mirroring the eval case).
	if err := os.WriteFile(filepath.Join(dir, "fz-notes.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCorpusTmpFixtures(t)
}

// writeCorpusTmpFixtures crafts the /tmp fixtures the executed corpus
// rows reference (curl configs, wget URL lists, a git exec source).
func writeCorpusTmpFixtures(t *testing.T) {
	t.Helper()
	for _, f := range []string{"/tmp/verify-fuzz-curlcfg", "/tmp/verify-fuzz-wgeturls"} {
		if err := os.WriteFile(f, []byte("probe\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f)
	}
	srcdir := "/tmp/verify-fuzz-gitexec"
	if err := os.MkdirAll(srcdir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcdir)
	for _, f := range []string{"/tmp/verify-fuzz-src", "/tmp/verify-fuzz-fetched"} {
		if err := os.WriteFile(f, []byte("exit 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f)
	}
}

// TestCorpusTally runs the full adversarial corpus under Go and tallies
// strict/pass/xfail results per the TSV contract.
func TestCorpusTally(t *testing.T) {
	rows := readCorpus(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCorpusFixtures(t, dir)
	counts := map[string]int{}
	for i, r := range rows {
		pr := fmt.Sprintf("corpus%d", i)
		writeCorpusPatch(t, dir, pr, r.cmd)
		switch gradeCorpusRow(t, dir, pr, r) {
		case "strict":
			counts["strict"]++
		case "pass":
			counts["pass"]++
		default:
			counts["xfail"]++
		}
	}
	t.Logf("corpus under Go: strict-refuse=%d pass=%d xfail-exec=%d (total %d)",
		counts["strict"], counts["pass"], counts["xfail"], len(rows))
}

// writeCorpusPatch writes the patch.json claim for one corpus row.
func writeCorpusPatch(t *testing.T, dir, pr, cmd string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", pr)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "{\"verifyCmd\":" + jsonQuote(cmd) + "}"
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gradeCorpusRow grades one corpus row per the TSV contract and returns
// its tally bucket.
func gradeCorpusRow(t *testing.T, dir, pr string, r corpusRow) string {
	t.Helper()
	switch r.expect + ":" + r.mode {
	case "PASS:strict":
		corpusGradeExec(t, dir, pr)
		return "pass"
	case "verify-destructive:strict", "verify-networked:strict",
		"verify-credential-read:strict", "verify-repo-escape:strict":
		before, err := os.ReadFile(filepath.Join(dir, ".review", pr, "patch.json"))
		if err != nil {
			t.Fatalf("%s: patch.json unreadable: %v", pr, err)
		}
		corpusGradeRefuse(t, dir, pr, r.expect, before)
		return "strict"
	default:
		// xfail: executing is the recorded expectation.
		corpusGradeExec(t, dir, pr)
		return "xfail"
	}
}
