package backtest

import (
	"bytes"
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// TestMineResolvesAmbiguousRef proves a --since name that is both a tag and a
// branch resolves. git prints an ambiguity warning on stderr beside the sha on
// stdout; the miner must read the sha, not the warning.
func TestMineResolvesAmbiguousRef(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	b.r.git("branch", "amb", b.base)
	b.r.git("tag", "amb", b.base)
	data := runMine(t, b.r, "amb")
	if data.Since.SHA != b.base {
		t.Errorf("since sha = %s; want %s", data.Since.SHA, b.base)
	}
	if len(data.Mine.Candidates) != 1 {
		t.Errorf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
}

// noisyCommander runs each command for real and prepends a warning to its
// stderr, the way git prints ambiguity and hint lines beside real output.
type noisyCommander struct{ inner core.Commander }

// Run delegates and adds the warning to the stderr stream only.
func (c noisyCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	res, err := c.inner.Run(ctx, cmd)
	res.Stderr = "warning: refname 'amb' is ambiguous.\nhint: noise\n" + res.Stderr
	return res, err
}

// TestMineStderrNoiseDoesNotCorruptParses proves warnings on stderr leave the
// parsed shas, blame lines, and therefore mine.json untouched.
func TestMineStderrNoiseDoesNotCorruptParses(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	_, clean := mineRun(t, b.r, b.base, nil)
	resp, ref := Run(t.Context(), &Request{
		Runtime: &core.Runtime{
			Commander: noisyCommander{inner: coreexec.Runner{MissingTool: true}},
		},
		RepoRoot: b.r.dir, Mode: modeMine, Since: b.base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if got := readMine(t, b.r, resp.Data.Run); !bytes.Equal(clean, got) {
		t.Errorf("mine.json differs with stderr noise:\n%s\n---\n%s", clean, got)
	}
}
