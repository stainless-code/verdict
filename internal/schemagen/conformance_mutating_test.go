package schemagen

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
)

// fixedCommander replays canned stdout per exact argv and fails the test on
// any unscripted spawn, so the mutating-verb conformance runs stay hermetic.
type fixedCommander struct {
	out map[string]string
	t   *testing.T
}

// Run replays the scripted stdout for argv.
func (c fixedCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	c.t.Helper()
	key := strings.Join(cmd.Argv, "\x00")
	out, ok := c.out[key]
	if !ok {
		c.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{}, fmt.Errorf("unexpected argv: %q", cmd.Argv)
	}
	return core.Result{Stdout: out}, nil
}

// fixRuntime builds the hermetic fix runtime: a clean worktree audit.
func fixRuntime(t *testing.T) *core.Runtime {
	t.Helper()
	return &core.Runtime{
		Now: fixedNow,
		Commander: fixedCommander{t: t, out: map[string]string{
			"git\x00ls-files\x00-u":              "",
			"git\x00status\x00--porcelain\x00-z": "",
		}},
	}
}

// TestRuntimeFixEnvelopeValidates proves the fix response envelope validates
// against the generated envelope schema and carries the typed pass profile.
func TestRuntimeFixEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/local-c1/findings.json", "["+fixtureFinding()+"]")
	req := &fixcore.Request{Human: io.Discard}
	req.ReadRequest = core.ReadRequest{Runtime: fixRuntime(t), Repo: "acme/app", Pr: "local-c1"}
	resp, ref := fixcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("fix refusal: %v", ref)
	}
	if resp.Data.Complete || resp.Data.Pass == nil || *resp.Data.Pass != 1 {
		t.Errorf("fix pass data = %+v", resp.Data)
	}
	assertEnvelope(t, doc, "fix", "local-c1", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
}

// TestRuntimeLearnEnvelopeValidates proves the learn status envelope
// validates against the generated envelope schema and carries typed rules.
func TestRuntimeLearnEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/kb.jsonl",
		`{"id":"await-err","rule":"Check await paths","scope":"src/*.ts",`+
			`"provenance":"manual","rigor":"walked","hits":2,"false_hits":0,`+
			`"judged":2,"status":"active"}`+"\n")
	req := &learncore.Request{Status: true}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime()}
	resp, ref := learncore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("learn refusal: %v", ref)
	}
	if len(resp.Data.Rules) != 1 {
		t.Errorf("learn rules = %v", resp.Data.Rules)
	}
	assertEnvelope(t, doc, "learn", "", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
}

// TestVerifyDataProfileValidates pins the verify data profile against the
// generator: the struct is the wire source, so this fails when a tag moves a
// property out of its published name or required list.
func TestVerifyDataProfileValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	data := &core.VerifyData{
		Exit:       0,
		TimedOut:   false,
		OutputTail: "ok\n",
		Patch:      "diff --git a/notes.txt b/notes.txt\n",
	}
	validateWire(t, doc, "verify data", defOf(t, doc, "verifyData"), decodeWire(t, data.Wire()))

	var modeled VerifyData
	unmarshalWire(t, data.Wire(), &modeled)
	if modeled.Exit != 0 || modeled.TimedOut || modeled.OutputTail != "ok\n" || modeled.Patch == "" {
		t.Errorf("verify data round-trip = %+v", modeled)
	}
}

// TestRuntimeInitEnvelopeValidates proves the init response envelope
// validates against the generated envelope schema and carries the preset
// values as numbers.
func TestRuntimeInitEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	req := &initcore.Request{
		Preset: "standard", Yes: true, Human: io.Discard, Diag: io.Discard,
		ReadAnswer: func() string { return "" },
	}
	runtime := fixedRuntime()
	runtime.Commander = fixedCommander{t: t, out: map[string]string{
		"git\x00status\x00--porcelain": "",
	}}
	req.ReadRequest = core.ReadRequest{Runtime: runtime}
	resp, ref := initcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("init refusal: %v", ref)
	}
	if resp.Data.Preset != "standard" || !resp.Data.CheckTreeClean {
		t.Errorf("init data = %+v", resp.Data)
	}
	assertEnvelope(t, doc, "init", "", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
}
