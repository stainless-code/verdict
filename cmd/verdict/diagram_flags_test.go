package main

import (
	"strings"
	"testing"
)

func TestRemovedDiagramFlags(t *testing.T) {
	hermeticEnv(t)
	for _, flag := range []string{"--diagram-behavioral", "--no-diagram-behavioral", "--diagram-expanded", "--no-diagram-expanded"} {
		t.Run(flag, func(t *testing.T) {
			code, _, stderr := runCapture(t, []string{"report", "acme/app", "1", flag})
			if code != 2 || !strings.Contains(stderr, "unknown flag '"+flag+"'") {
				t.Fatalf("exit = %d, stderr = %q; want exit 2 and unknown flag", code, stderr)
			}
		})
	}
}
