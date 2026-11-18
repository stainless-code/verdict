package drain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

func planFeed() *threadscore.File {
	return &threadscore.File{Threads: []threadscore.Thread{
		thread("t1", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r1", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w1", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("t2", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r2", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w2", "dependabot[bot]", threadscore.AuthorBot, "2026-11-02T00:00:00Z")),
		thread("t3", threadscore.KindReview, "alice", threadscore.AuthorHuman, false, false,
			comment("r3", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
	}}
}

func TestNewPlanPinsDigestAndSkips(t *testing.T) {
	t.Parallel()
	feed := planFeed()
	plan, deferred := NewPlan("github", 7, "head", "2026-11-03T00:00:00Z", "verdict-bot", feed, nil, 20)
	if len(deferred) != 0 || len(plan.Pending) != 1 || plan.Pending[0].ThreadID != "t1" {
		t.Fatalf("plan pending = %+v, deferred = %v", plan.Pending, deferred)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0] != (Skip{ThreadID: "t2", Reason: ReasonBotReply}) {
		t.Errorf("plan skipped = %+v", plan.Skipped)
	}
	if plan.OtherThreads != 1 || plan.SchemaVersion != SchemaVersion || plan.Provider != "github" ||
		plan.HeadSHA != "head" || plan.Self != "verdict-bot" {
		t.Errorf("plan envelope fields drifted: %+v", plan)
	}
	if len(plan.PlanDigest) != 64 {
		t.Fatalf("plan_digest = %q, want a 64-hex sha256", plan.PlanDigest)
	}
}

func TestNewPlanDigestPinsWaitingComment(t *testing.T) {
	t.Parallel()
	feed := planFeed()
	first, _ := NewPlan("github", 7, "head", "2026-11-03T00:00:00Z", "verdict-bot", feed, nil, 20)
	feed.Threads[0].Comments[1].ID = "w9"
	changed, _ := NewPlan("github", 7, "head", "2026-11-03T00:00:00Z", "verdict-bot", feed, nil, 20)
	if changed.PlanDigest == first.PlanDigest {
		t.Error("a new waiting comment kept the same plan_digest")
	}
}

func writeDrainProcedureDecoys(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"PROCEDURE.md", "prompts/system.md", "prompts/address-reviews.md"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("disk decoy "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func embeddedDrainProcedure(t *testing.T) []byte {
	t.Helper()
	var want []byte
	for _, name := range []string{
		skillfs.ProcedureMD, "prompts/system.md", "prompts/address-reviews.md",
	} {
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		want = append(want, raw...)
	}
	return want
}

// TestWriteProcedureUsesEmbeddedPayload pins the drain assembly against the
// three embedded files and proves disk copies cannot replace any of them.
func TestWriteProcedureUsesEmbeddedPayload(t *testing.T) {
	dir := t.TempDir()
	writeDrainProcedureDecoys(t, dir)
	t.Chdir(dir)

	want := embeddedDrainProcedure(t)
	if err := WriteProcedure(dir); err != nil {
		t.Fatalf("WriteProcedure: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, KeyProcedureMD))
	if err != nil {
		t.Fatalf("read %s: %v", KeyProcedureMD, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not the embedded drain assembly", KeyProcedureMD)
	}
}
