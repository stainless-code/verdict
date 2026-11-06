package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// RunState is the persisted per-run typed bundle: the fetch-time capture
// plus the resolved requirements, plan, spend, and privacy views the
// later verbs enforce, with check observations appended as execution
// evidence arrives. Stage flags and consents stay in their own
// invocation records; outcomes never rewrite captured inputs.
type RunState struct {
	Resolved      *Resolved          `json:"resolved"`
	SchemaVersion string             `json:"schema_version"`
	Name          string             `json:"name"`
	BaseSHA       string             `json:"base_sha"`
	CapturedAt    string             `json:"captured_at"`
	ConfigSHA256  string             `json:"config_sha256"`
	Evidence      Evidence           `json:"evidence"`
	Outcomes      []CheckObservation `json:"outcomes,omitempty"`
	Root          RequirementSet     `json:"root"`
	Trusted       bool               `json:"trusted"`
}

// runStateFileName is the one file inside the per-run state directory.
const runStateFileName = "run.json"

// runSchemaVersion pins the run-state file shape.
const runSchemaVersion = "1"

// WriteRunState persists the run bundle under `.verdict/state/<name>/`,
// ensuring the ignore-first layout before any write. The directory and
// file land confined: a symlinked state root refuses instead of writing
// outside the repository.
func WriteRunState(root, name string, rs *RunState) *stateError {
	layout := NewLayout(root)
	if err := layout.EnsureStateIgnore(); err != nil {
		return err
	}
	if err := layout.EnsureStateDir(); err != nil {
		return err
	}
	rs.SchemaVersion = runSchemaVersion
	rs.Name = name
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return &stateError{msg: "could not encode run state for " + name, cause: err}
	}
	path := filepath.Join(layout.StateDir(), name, runStateFileName)
	if _, serr := confineJoin(mustConfineRoot(root), filepath.Join(VerdictDir, StateDirName, name)); serr != nil {
		return serr
	}
	if err := os.MkdirAll(filepath.Join(layout.StateDir(), name), stateDirPerm); err != nil {
		return &stateError{msg: "could not create run state directory " + name, cause: err}
	}
	if werr := atomic.WriteFile(path, append(raw, '\n')); werr != nil {
		return &stateError{msg: "could not write " + path, cause: werr}
	}
	return nil
}

// mustConfineRoot resolves the root for the run directory confinement
// check, falling back to the raw root when resolution fails (the
// subsequent MkdirAll surfaces the real error).
func mustConfineRoot(root string) string {
	real, err := confineRoot(ifEmpty(root))
	if err != nil {
		return ifEmpty(root)
	}
	return real
}

// ifEmpty defaults an empty root to the working directory, matching
// NewLayout.
func ifEmpty(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// ReadRunState loads the persisted run bundle: absent reads (nil, false,
// nil) so legacy runs degrade to no repository policy instead of
// refusing. Present but malformed state refuses loudly: the operator
// re-fetches or removes it.
func ReadRunState(root, name string) (*RunState, bool, *stateError) {
	layout := NewLayout(root)
	path := filepath.Join(layout.StateDir(), name, runStateFileName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &stateError{msg: "could not read " + path, cause: err}
	}
	var rs RunState
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, false, &stateError{msg: path + " is not a run bundle (re-fetch to rebuild it)", cause: err, operator: true}
	}
	if rs.SchemaVersion != runSchemaVersion {
		return nil, false, &stateError{msg: path + " has schema_version " + rs.SchemaVersion + " (want 1; re-fetch to rebuild it)", operator: true}
	}
	return &rs, true, nil
}

// AppendOutcome records one observed check execution on an existing run
// bundle, preserving every captured input byte-for-byte. Absent run
// state appends nothing (false, nil): legacy runs carry no typed policy,
// and inventing one from an outcome would reconstruct inputs the fetch
// never captured. The error return is the sealed state error as an
// error so verb packages can warn without naming the type.
func AppendOutcome(root, name string, obs *CheckObservation) (bool, error) {
	rs, found, serr := ReadRunState(root, name)
	if serr != nil {
		return false, serr
	}
	if !found {
		return false, nil
	}
	rs.Outcomes = append(rs.Outcomes, *obs)
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return false, &stateError{msg: "could not encode run state for " + name, cause: err}
	}
	layout := NewLayout(root)
	path := filepath.Join(layout.StateDir(), name, runStateFileName)
	if werr := atomic.WriteFile(path, append(raw, '\n')); werr != nil {
		return false, &stateError{msg: "could not write " + path, cause: werr}
	}
	return true, nil
}

// inventoryDirName holds the trusted-base support snapshot beside the
// run bundle.
const inventoryDirName = "inventory"

// WriteInventory stores the trusted-base support bytes beside the run
// bundle: the configuration, knowledge corpus, learnings, and override
// fragments the resolution consumed. Layout paths shed their `.verdict/`
// prefix, so the snapshot reads `inventory/config.json`. Writes stay
// confined under the state directory.
func WriteInventory(root, name string, files map[string][]byte) *stateError {
	layout := NewLayout(root)
	base := filepath.Join(layout.StateDir(), name, inventoryDirName)
	names := make([]string, 0, len(files))
	for path := range files {
		names = append(names, path)
	}
	slices.Sort(names)
	for _, path := range names {
		rel := strings.TrimPrefix(path, VerdictDir+"/")
		rel = filepath.FromSlash(rel)
		target := filepath.Join(base, rel)
		realRoot, err := confineRoot(ifEmpty(root))
		if err != nil {
			return &stateError{msg: "could not resolve repository root " + root, cause: err}
		}
		if _, serr := confineJoin(realRoot, filepath.Join(VerdictDir, StateDirName, name, inventoryDirName, rel)); serr != nil {
			return serr
		}
		if err := os.MkdirAll(filepath.Dir(target), stateDirPerm); err != nil {
			return &stateError{msg: "could not create inventory directory for " + path, cause: err}
		}
		if werr := atomic.WriteFile(target, files[path]); werr != nil {
			return &stateError{msg: "could not write inventory " + path, cause: werr}
		}
	}
	return nil
}

// SnapshotInventory reads the trusted-base inventory beside the
// configuration: the tracked support files plus every override fragment,
// each capped before any read. Missing files read absent; the snapshot
// records exact bytes for evidence hashing, never worktree edits.
func SnapshotInventory(ctx context.Context, cmd core.Commander, repo, baseSHA string) (map[string][]byte, []string, *core.RefusalError) {
	paths := append([]string{}, Inventory...)
	overrides, warnings, ref := listOverrides(ctx, cmd, repo, baseSHA)
	if ref != nil {
		return nil, nil, ref
	}
	paths = append(paths, overrides...)
	contents, ref := controls.ReadPaths(ctx, cmd, repo, baseSHA, paths)
	if ref != nil {
		return nil, nil, ref
	}
	out := map[string][]byte{}
	for _, path := range Inventory {
		if raw, ok := contents[path]; ok {
			out[path] = raw
		}
	}
	for _, path := range overrides {
		if raw, ok := contents[path]; ok {
			out[path] = raw
		}
	}
	return out, warnings, nil
}

// maxOverrideFiles caps the override snapshot: support fragments are
// small by contract, and the snapshot must not balloon the bundle.
const maxOverrideFiles = 100

// listOverrides names the tracked override fragments under the base
// commit: an absent overrides directory is not an error.
func listOverrides(ctx context.Context, cmd core.Commander, repo, base string) ([]string, []string, *core.RefusalError) {
	res, err := cmd.Run(ctx, core.Command{Argv: []string{"git", "ls-tree", "-r", "--name-only", base, "--", OverridesDir + "/"}})
	if err != nil {
		return nil, nil, &core.RefusalError{Code: controls.CodeUnavailable, Message: fmt.Sprintf("could not list %s at base %s: %v", OverridesDir, shortSHA(base), err)}
	}
	if res.ExitCode != 0 {
		return nil, nil, nil
	}
	var names []string
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	if len(names) > maxOverrideFiles {
		return names[:maxOverrideFiles], []string{fmt.Sprintf("overrides snapshot truncated to %d files", maxOverrideFiles)}, nil
	}
	return names, nil, nil
}

// shortSHA renders the short commit label for messages.
func shortSHA(full string) string {
	if len(full) > 7 {
		return full[:7]
	}
	return full
}

// SnapshotWorktreeInventory reads the same inventory from the working
// tree for fetch-local runs, where the reviewed content is local.
// Absent files read absent.
func SnapshotWorktreeInventory(root string) map[string][]byte {
	out := map[string][]byte{}
	for _, path := range Inventory {
		raw, err := os.ReadFile(filepath.Join(ifEmpty(root), path))
		if err == nil {
			out[path] = raw
		}
	}
	entries, err := os.ReadDir(filepath.Join(ifEmpty(root), OverridesDir))
	if err != nil {
		return out
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	for i, name := range names {
		if i >= maxOverrideFiles {
			break
		}
		raw, err := os.ReadFile(filepath.Join(ifEmpty(root), OverridesDir, name))
		if err == nil {
			out[filepath.Join(OverridesDir, name)] = raw
		}
	}
	return out
}
