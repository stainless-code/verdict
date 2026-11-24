package lsp

import (
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestWatchChangeRepublishes pins the live path: with the watcher on, an
// out-of-band rewrite of the bundle's findings store (a terminal `verdict
// triage` or `record`) republishes the open document without any client
// message, clearing the finding that is no longer open.
func TestWatchChangeRepublishes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.NoWatch = false
	cfg.Interval = 10 * time.Millisecond
	cfg.Sweep = 20 * time.Millisecond
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	uri := fileURI(root, "notes.txt")
	openDoc(s, uri, 3, notesText)
	mark := len(s.barrier(2, 0)) + 1
	writeBundle(t, root, "local-1",
		findingJSON(sigCrit, "wont-fix", "critical", "notes.txt", 2),
		findingJSON(sigImp, "open", "important", "other.go", 4),
	)
	testenv.Until(t, func() bool {
		for _, p := range publishes(t, s.snapshot()[mark:]) {
			if p.URI == uri && len(p.Diagnostics) == 0 && p.Version != nil && *p.Version == 3 {
				return true
			}
		}
		return false
	})
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

// TestNoWatchNeverSubscribes pins --no-watch structurally at the seam: the
// session never asks the read model for changes, while the default posture
// subscribes once at initialized.
func TestNoWatchNeverSubscribes(t *testing.T) {
	t.Parallel()
	for _, noWatch := range []bool{true, false} {
		root := fixtureRoot(t)
		cfg := newConfig(root)
		cfg.NoWatch = noWatch
		counting := &countingModel{ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)})}
		cfg.Layer = counting
		s := startSession(t, cfg)
		s.initialize(1, nil)
		s.notify(methodInitialized, map[string]any{})
		s.barrier(2, 0)
		if err := s.finish(3); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{true: 0, false: 1}[noWatch]; counting.subscriptions() != want {
			t.Fatalf("NoWatch=%v: Changes called %d times, want %d", noWatch, counting.subscriptions(), want)
		}
	}
}
