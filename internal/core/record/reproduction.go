package recordcore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/repro"
)

const fieldReproduction = "reproduction"

const reproductionBudget = 1 << 20

func (st *state) captureReproductions() {
	remaining := reproductionBudget
	for _, item := range st.entries {
		entry, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		remaining -= st.captureEntryReproduction(entry, remaining)
	}
}

func captureReproduction(dir, sig string, raw any, dry bool, remaining int) (any, int, error) {
	input, ok := raw.(*jq.Obj)
	if !ok || input == nil {
		return nil, 0, jq.Sealed("reproduction", errors.New("supply reproduction.path and reproduction.command"))
	}
	path, _ := input.Get("path")
	command, _ := input.Get("command")
	pathString, pathOK := path.(string)
	commandString, commandOK := command.(string)
	if !pathOK || !commandOK {
		return nil, 0, jq.Sealed("reproduction", errors.New("reproduction path and command must be strings"))
	}
	if remaining < repro.MaxBytes {
		return nil, 0, jq.Sealed("reproduction", errors.New("bundle reproduction budget exhausted"))
	}
	commandString, err := retainedCommand(dir, sig, pathString, commandString, input.Has("redacted"))
	if err != nil {
		return nil, 0, jq.Sealed("capture reproduction", err)
	}
	receipt, err := repro.Capture(dir, sig, pathString, commandString, dry)
	if err != nil {
		return nil, 0, jq.Sealed("capture reproduction", err)
	}
	return jq.NewObj("path", receipt.Path, "command", receipt.Command, "redacted", receipt.Redacted), repro.MaxBytes, nil
}

func (st *state) captureEntryReproduction(entry *jq.Obj, remaining int) int {
	raw, _ := entry.Get(fieldReproduction)
	entry.Delete(fieldReproduction)
	rung, _ := entry.Get(fieldRigor)
	if rung != rungReproduced || !st.draftsEnabled() {
		return 0
	}
	sig, _ := sigString(entry)
	receipt, used, err := captureReproduction(st.dir, sig, raw, !st.persistsFindings(), remaining)
	if err != nil {
		st.warn(fmt.Sprintf("reproduced finding %s has no runnable artifact: %s", sig, envsecret.ScrubShapes(err.Error())))
		return 0
	}

	entry.Set(fieldReproduction, receipt)
	return used
}

// draftsEnabled reports whether regression-test drafting retains reproduced
// artifacts: tests.draft off disables drafting, on-reproduce (and the unset
// default) keeps the shipped behavior. The fetch-captured resolution is
// authoritative; bundles without one fall back to the live base config.
func (st *state) draftsEnabled() bool {
	if st.run != nil && st.run.Resolved != nil {
		return st.run.Resolved.Tests == nil || st.run.Resolved.Tests.Draft != "off"
	}
	return st.liveDraftsEnabled()
}

func (st *state) liveDraftsEnabled() bool {
	if st.controls == nil {
		return true
	}
	raw, present := st.controls.ReadFile(config.ConfigPath)
	if !present {
		return true
	}
	cfg, ref := config.ParseBase(&config.BaseConfig{Raw: raw, Present: true})
	if ref != nil {
		// An unparsable base config already refused on its own path;
		// drafting keeps the default rather than adding a second refusal.
		return true
	}
	return cfg.Tests == nil || cfg.Tests.Draft != "off"
}

func retainedCommand(dir, sig, path, command string, retained bool) (string, error) {
	if !retained {
		return command, nil
	}
	if _, err := repro.Read(dir, sig, path); err != nil {
		return "", jq.Sealed("read retained reproduction", err)
	}
	return strings.ReplaceAll(command, path, "{artifact}"), nil
}
