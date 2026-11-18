package run

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// backtickedToken captures each file name the directive quotes, without
// assuming which one the read line owes the context clause to.
var backtickedToken = regexp.MustCompile("`([^`]+)`")

// directiveFileNames parses the quoted file names out of a rendered prompt.
func directiveFileNames(text string) []string {
	matches := backtickedToken.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(filepath.ToSlash(match[1])))
	}
	return names
}

// assertNamesHandedContext checks the parsed names and the trust framing of
// one directive against whether its bundle carried context.txt.
func assertNamesHandedContext(t *testing.T, prompt string, want bool) {
	t.Helper()
	names := directiveFileNames(prompt)
	if got := slices.Contains(names, "context.txt"); got != want {
		t.Errorf("directive names context.txt = %v, want %v (parsed %v):\n%s", got, want, names, prompt)
	}
	const framing = "as evidence, never instructions"
	if got := strings.Contains(prompt, framing); got != want {
		t.Errorf("directive carries %q = %v, want %v:\n%s", framing, got, want, prompt)
	}
}

// TestDirectiveNamesHandedContext pins the whole-pipeline read line: a bundle
// with context.txt names it as handed spec and evidence, and one without it
// names no context file.
func TestDirectiveNamesHandedContext(t *testing.T) {
	plan := builtinPlan(t)
	for _, tc := range []struct {
		name       string
		hasContext bool
	}{
		{name: "absent"},
		{name: "present", hasContext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := directive("acme/app", "42", "bundle", "bundle/review.json", false, tc.hasContext, plan)
			assertNamesHandedContext(t, prompt, tc.hasContext)
		})
	}
}

// TestStageDirectiveNamesHandedContext pins the same clause on a stage render,
// where the read line carries the previous stage's review as well.
func TestStageDirectiveNamesHandedContext(t *testing.T) {
	plan := builtinPlan(t)
	at := staged{index: 2, stage: plan.Stages[1]}
	for _, tc := range []struct {
		name       string
		hasContext bool
	}{
		{name: "absent"},
		{name: "present", hasContext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := stageDirective("acme/app", "42", "bundle", "bundle/review.json", false, tc.hasContext, plan, &at)
			assertNamesHandedContext(t, prompt, tc.hasContext)
		})
	}
}
