package config

import (
	"context"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// Inventory lists the trusted-base configuration inputs. The D1 snapshot
// caps apply first: above 256 KiB the file refuses as controls-too-large
// before the configuration parser runs. Supporting inputs ride beside
// the configuration; only local state is ignored, never these.
var Inventory = []string{ConfigPath, KBPath, LearningsPath}

// BaseConfig is one trusted-base configuration read: Present reports
// whether `.verdict/config.json` exists at the base, and Raw carries its
// exact bytes for the strict parser and the evidence hash.
type BaseConfig struct {
	Raw     []byte
	Present bool
}

// ReadBase reads the typed configuration at the PR base commit through
// the shipped D1 seam: the resolver never fetches, head edits never move
// the read, and no reference selector exists in configuration. Absent
// base configuration is not an error: it means no repository policy, and
// the run follows the no-init rule on personal and built-in defaults.
// Present but malformed input parses (and refuses) downstream; this
// adapter never creates shared configuration as a side effect of a read.
func ReadBase(ctx context.Context, cmd core.Commander, repo, root, base string) (*BaseConfig, *core.RefusalError) {
	return readBase(ctx, cmd, repo, root, base, false)
}

// ReadBaseOptional reads the typed configuration for a run whose repository
// layer is absent by construction (verdict demo's URL form: no checkout to
// read at the PR base). A base git cannot supply reads as no repository
// policy, exactly like an absent `.verdict/config.json`, and the contents API
// is never reached: the run has no host credential it may spend on controls.
func ReadBaseOptional(ctx context.Context, cmd core.Commander, repo, root, base string) (*BaseConfig, *core.RefusalError) {
	return readBase(ctx, cmd, repo, root, base, true)
}

// readBase reads one base configuration through the shipped D1 seam, with the
// optional posture selecting the repository-layer-absent read.
//
// The optional posture answers "no repository policy" without touching git at
// all: root names wherever the run was anchored, and when verdict demo's URL
// form runs inside a clone that happens to hold the PR's base commit, that
// checkout's `.verdict/config.json` is not this pull request's policy.
func readBase(ctx context.Context, cmd core.Commander, repo, root, base string, optional bool) (*BaseConfig, *core.RefusalError) {
	if optional {
		return &BaseConfig{}, nil
	}
	contents, ref := controls.ReadPaths(ctx, cmd, repo, root, base, []string{ConfigPath})
	if ref != nil {
		return nil, ref
	}
	raw, ok := contents[ConfigPath]
	if !ok {
		return &BaseConfig{}, nil
	}
	return &BaseConfig{Present: true, Raw: raw}, nil
}

// ParseBase parses a trusted-base read: absent reads as no repository
// policy (nil configuration, no refusal), present reads through the
// strict parser in its fixed refusal order.
func ParseBase(found *BaseConfig) (*core.Config, *core.RefusalError) {
	if found == nil || !found.Present {
		return nil, nil
	}
	return Parse(found.Raw)
}
