package drain

import (
	"testing"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

func TestNewPlanPinsDigestAndSkips(t *testing.T) {
	t.Parallel()
	feed := &threadscore.File{Threads: []threadscore.Thread{
		thread("t1", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r1", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w1", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("t2", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r2", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w2", "dependabot[bot]", threadscore.AuthorBot, "2026-11-02T00:00:00Z")),
		thread("t3", threadscore.KindReview, "alice", threadscore.AuthorHuman, false, false,
			comment("r3", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
	}}
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
	feed.Threads[0].Comments[1].ID = "w9"
	changed, _ := NewPlan("github", 7, "head", "2026-11-03T00:00:00Z", "verdict-bot", feed, nil, 20)
	if changed.PlanDigest == plan.PlanDigest {
		t.Error("a new waiting comment kept the same plan_digest")
	}
}
