package testkit_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testkit"
)

func TestScriptSnapshotsCommand(t *testing.T) {
	s := &testkit.Script{T: t, Results: map[string]core.Result{
		"git\x00status": {},
		"git\x00diff":   {},
	}}
	cmd := &core.Command{Argv: []string{"git", "status"}}
	if _, err := s.Run(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	cmd.Argv[1] = "diff"
	if _, err := s.Run(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	cmd.Argv = []string{"git", "log"}
	if len(s.Calls) != 2 {
		t.Fatalf("Calls length = %d; want 2", len(s.Calls))
	}
	if !slices.Equal(s.Calls[0].Argv, []string{"git", "status"}) {
		t.Errorf("first call argv = %q; want [git status]", s.Calls[0].Argv)
	}
	if !slices.Equal(s.Calls[1].Argv, []string{"git", "diff"}) {
		t.Errorf("second call argv = %q; want [git diff]", s.Calls[1].Argv)
	}
}

func TestScriptRejectsCancelledContext(t *testing.T) {
	s := &testkit.Script{T: t, Results: map[string]core.Result{
		"git\x00status": {},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Run(ctx, &core.Command{Argv: []string{"git", "status"}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run error = %v; want context.Canceled", err)
	}
	if len(s.Calls) != 0 {
		t.Errorf("Calls length = %d; want 0", len(s.Calls))
	}
}
