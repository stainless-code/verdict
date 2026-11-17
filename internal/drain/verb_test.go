package drain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

type stubCommander struct{}

func (*stubCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

func TestNextAfterAgentDoesNotRecommendPost(t *testing.T) {
	t.Parallel()
	got := next(Request{Repo: "acme/app", Pr: "7"}, &flags{agent: "claude"})
	if !strings.Contains(got, draincore.KeyDraftsJSON) || strings.Contains(got, "--post") {
		t.Errorf("next = %q, want an available step naming %s with no --post", got, draincore.KeyDraftsJSON)
	}
}

func TestRefreshRequestKeepsInjectedCommander(t *testing.T) {
	t.Parallel()
	cmdr := &stubCommander{}
	req := Request{Runtime: &core.Runtime{Commander: cmdr}}
	r := &cli.Runner{Now: time.Now}
	got := refreshRequest(r, req, &flags{provider: "github"})
	if got.Commander != cmdr {
		t.Errorf("refresh commander = %T, want the injected commander", got.Commander)
	}
}
