package diagram_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/diagram"
)

// TestAbsent checks the no-diagram body passes through untouched.
func TestAbsent(t *testing.T) {
	res := diagram.Validate("plain body, no fences", false, false, "<!-- verdict-diagram -->")
	if res.Status.State != "absent" {
		t.Errorf("state = %q; want absent", res.Status.State)
	}
	if res.Body != "plain body, no fences" {
		t.Errorf("body changed: %q", res.Body)
	}
}

// TestStrippedWhenOptedOut checks opted-out diagrams are cut to a drop note.
func TestStrippedWhenOptedOut(t *testing.T) {
	body := "top\n```mermaid\nflowchart TD\n```\nbottom"
	res := diagram.Validate(body, false, false, "<!-- verdict-diagram -->")
	if res.Status.State != "stripped" {
		t.Errorf("state = %q; want stripped", res.Status.State)
	}
	if strings.Contains(res.Body, "```mermaid") {
		t.Errorf("fence survived stripping: %q", res.Body)
	}
	if len(res.Warns) != 1 {
		t.Errorf("warns = %v; want 1", res.Warns)
	}
}

// TestKeptWhenOptedIn checks opted-in diagrams survive with a kept detail.
func TestKeptWhenOptedIn(t *testing.T) {
	body := "top\n```mermaid\nflowchart TD\n```\nbottom"
	res := diagram.Validate(body, true, false, "<!-- verdict-diagram -->")
	if res.Status.State != "kept" {
		t.Errorf("state = %q; want kept", res.Status.State)
	}
	if !strings.Contains(res.Body, "```mermaid") {
		t.Errorf("kept block lost: %q", res.Body)
	}
	if res.Status.Keyword != "flowchart" {
		t.Errorf("keyword = %q; want flowchart", res.Status.Keyword)
	}
}

// TestUnknownKeywordDropped checks unknown keywords are dropped with a warning.
func TestUnknownKeywordDropped(t *testing.T) {
	body := "top\n```mermaid\npie chart\n```\nbottom"
	res := diagram.Validate(body, true, false, "<!-- verdict-diagram -->")
	if res.Status.State != "dropped" {
		t.Errorf("state = %q; want dropped", res.Status.State)
	}
	if strings.Contains(res.Body, "pie chart") {
		t.Errorf("unknown block survived: %q", res.Body)
	}
}
