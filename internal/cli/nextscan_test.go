package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNextScannerJSONAndTruncatedEnvelope(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	scan := NewNextScanner(&out)
	payload := `{"schema_version":"1","verb":"report","repo":"acme/app","pr":null,"at":"2026-11-19T10:00:00Z","next":"run triage","warnings":[],"data":{},"error":null}` + "\n"
	if _, err := scan.Write([]byte(payload)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.String() != payload {
		t.Fatalf("forwarded bytes differed from input")
	}
	if got := scan.Next(true); got != "run triage" {
		t.Fatalf("Next(json) = %q, want %q", got, "run triage")
	}

	// Oversized JSON envelope (> 256 KiB): prefix scanner still reads top-level "next".
	var big bytes.Buffer
	bigScan := NewNextScanner(&big)
	huge := `{"schema_version":"1","verb":"fetch","repo":"acme/app","pr":7,"at":"2026-11-19T10:00:00Z","next":"review the bundle","warnings":[],"data":{"blob":"` +
		strings.Repeat("x", nextScanCap+4096) + `"},"error":null}` + "\n"
	if _, err := bigScan.Write([]byte(huge)); err != nil {
		t.Fatalf("Write(huge): %v", err)
	}
	if got := bigScan.Next(true); got != "review the bundle" {
		t.Fatalf("Next(huge json) = %q, want %q", got, "review the bundle")
	}
}

func TestNextScannerHumanLinesAndANSI(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	scan := NewNextScanner(&out)
	lines := "summary: 2 findings\nnext: first hint\n\x1b[36mnext: report or triage\x1b[0m\n"
	if _, err := scan.Write([]byte(lines)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := scan.Next(false); got != "report or triage" {
		t.Fatalf("Next(human) = %q, want %q", got, "report or triage")
	}
}
