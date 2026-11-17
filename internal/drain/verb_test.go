package drain

import (
	"context"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

type stubCommander struct{}

func (*stubCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
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
