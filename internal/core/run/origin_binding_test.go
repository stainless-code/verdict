package run

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type forgedOriginCommander struct {
	*phaseCommander
	self bool
}

func (p *forgedOriginCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	result, err := p.phaseCommander.Run(ctx, cmd)
	phase := p.phases[len(p.phases)-1]
	switch phase {
	case "x-finder-1":
		raw := strings.Replace(coordinatorReview, `"path":"a.go"`, `"origin":"finder-2","path":"a.go"`, 1)
		writeUnder(p.t, filepath.Join(p.dir, phase), "review.json", raw)
	case "x-finder-2":
		writeUnder(p.t, filepath.Join(p.dir, phase), "review.json", strings.TrimSuffix(coordinatorEmpty, "}")+`,"summary":"No defects found."}`)
	case "x-cross-2":
		if p.self {
			path := filepath.Join(p.dir, phase, "verdicts.json")
			receipt := readPhaseObject(p.t, path)
			rows := jq.Elements(readcommon.FieldOf(receipt, "results"))
			row, ok := rows[0].(*jq.Obj)
			if !ok {
				p.t.Fatal("cross result is not an object")
			}
			row.Set("checker", "finder-1")
			writeUnder(p.t, filepath.Join(p.dir, phase), "verdicts.json", receipt.MarshalCompact())
		}
	}
	return result, err
}

func TestCoordinatorBindsForgedOriginToTrustedOutput(t *testing.T) {
	for _, self := range []bool{false, true} {
		name := "opposite confirmation"
		if self {
			name = "forged self confirmation"
		}
		t.Run(name, func(t *testing.T) { assertForgedOrigin(t, self) })
	}
}

func assertForgedOrigin(t *testing.T, self bool) {
	t.Helper()
	req, base := coordinatorFixture(t)
	req.RoundsRaw = "2"
	req.Commander = &forgedOriginCommander{phaseCommander: base, self: self}
	_, ref := TwoFinders(t.Context(), req)
	if self {
		if ref == nil || ref.Code != "review-insufficient" {
			t.Fatalf("self confirmation refusal = %v", ref)
		}
	} else if ref != nil {
		t.Fatalf("%v; manifest=%s", ref, jq.Compact(readPhaseObject(t, filepath.Join(base.dir, "launcher.json"))))
	}
	assertOriginDispatch(t, base, self)
	entry := jq.NewObj("sig", "b02621c6e34339ac215c21bbc73527b558e69185", "status", "open", "severity", "important")
	execution := panelcore.CheckExecution(base.dir, nil)
	execution.Stamp([]any{entry})
	cross := readcommon.FieldOf(readcommon.FieldOf(entry, "verification"), "cross")
	if panelcore.EligibleCross(cross) == self {
		t.Fatalf("self=%v cross eligibility = %s", self, jq.Compact(cross))
	}
}

func assertOriginDispatch(t *testing.T, base *phaseCommander, self bool) {
	t.Helper()
	phases := strings.Join(base.phases, ",")
	want := "x-finder-1,x-finder-2,x-cross-2"
	if self {
		want = "x-finder-1,x-finder-2,x-cross-2,x-cross-2"
	}
	if phases != want {
		t.Fatalf("forged origin redirected dispatch: %s", phases)
	}
	plan := readPhaseObject(t, filepath.Join(base.dir, "verification", "plan.json"))
	if got := compactRows(jq.Elements(readcommon.FieldOf(plan, "checks"))); got != `[{"sig":"b02621c6e34339ac215c21bbc73527b558e69185","origin":"finder-1","checker":"finder-2"}]` {
		t.Fatalf("trusted origin plan = %s", got)
	}
}
