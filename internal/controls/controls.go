// Package controls snapshots the review control files at the PR base commit
// (`git show <base>:<path>` through [core.Commander], or the GitHub contents
// API when the checkout cannot supply the base) into a bundle; the one
// opt-out reads the worktree and is stamped in controls.json. post/record
// re-hash the snapshot and refuse when it is stale or missing. The inventory
// is the typed configuration plus its tracked shared support files; legacy
// `.review/` knobs are never read under any name.
package controls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Snapshot locations, caps (a control file is never truncated), refusal
// codes, and policies.
const (
	DirName       = "controls"
	FileName      = "controls.json"
	schemaVersion = "1"
	MaxFileBytes  = 256 << 10
	MaxTotalBytes = 1 << 20

	// CodeUnavailable: no or invalid snapshot, or the base object is not
	// reachable locally.
	CodeUnavailable = "controls-unavailable"
	// CodeStale: the snapshot base or a used-file hash does not match.
	CodeStale = "controls-stale"
	// CodeTooLarge: a control file exceeds its cap.
	CodeTooLarge = "controls-too-large"

	// PolicyBase is the default; PolicyHead is the recorded opt-out.
	PolicyBase = "base"
	PolicyHead = "head"

	statusUsed    = "used"
	statusMissing = "missing"
	dirPerm       = 0o755
)

// VerdictDir prefixes every typed control path at the Git root, and
// Inventory is the closed control set: the one typed configuration plus
// the tracked shared support files that ride beside it. The snapshot sheds
// this prefix, so control "config.json" is the tracked ".verdict/config.json".
const VerdictDir = ".verdict"

// Inventory is the closed control set (v1); new controls add rows.
var Inventory = []string{
	VerdictDir + "/config.json",
	VerdictDir + "/kb.jsonl",
	VerdictDir + "/learnings.md",
	VerdictDir + "/overrides/preamble.md",
	VerdictDir + "/overrides/footer-order.md",
	VerdictDir + "/overrides/walkthrough-threshold.md",
	VerdictDir + "/overrides/attribution.md",
}

// snapshotRel sheds the tracked directory prefix for the snapshot layout:
// snapshot "kb.jsonl" is the tracked ".verdict/kb.jsonl".
func snapshotRel(path string) string { return strings.TrimPrefix(path, VerdictDir+"/") }

// Request is one control resolution.
//
//nolint:govet // named request fields stay in call-site order
type Request struct {
	BundleDir string
	BaseSHA   string
	// Repo is the GitHub owner/repo whose contents API reads the base when
	// git cannot; "" keeps git the only source (other providers).
	Repo string
	// Root is the worktree the opt-out reads; "" is the working directory.
	Root string
	// Env carries the fetch environment: REVIEW_STRICT_INPUT=1 records the
	// strict-input posture for this run.
	Env map[string]string
	// AllowHead reads the worktree instead of the base commit.
	AllowHead bool
}

// Snapshot is one resolved control set plus its on-disk location.
type Snapshot struct {
	Dir  string
	File core.ControlsFile
}

// Resolve reads every inventory path from the PR base commit (or the
// worktree under the explicit opt-out) and writes the snapshot plus
// controls.json. It never falls back to the head on the default path.
func Resolve(ctx context.Context, cmd core.Commander, req *Request) (*Snapshot, *core.RefusalError) {
	snap := &Snapshot{Dir: filepath.Join(req.BundleDir, DirName), File: core.ControlsFile{
		SchemaVersion: schemaVersion, Policy: PolicyBase, BaseSHA: req.BaseSHA, SHA: req.BaseSHA,
	}}
	var contents map[string][]byte
	var ref *core.RefusalError
	if req.AllowHead {
		snap.File.Policy, snap.File.SHA = PolicyHead, ""
		contents, ref = readWorktree(req.Root)
	} else {
		contents, ref = readBase(ctx, cmd, req.Repo, req.Root, req.BaseSHA)
	}
	if ref == nil {
		snap.File.StrictInput = strictInput(req.Env)
		ref = snap.write(req.BundleDir, contents)
	}
	if ref != nil {
		return nil, ref
	}
	return snap, nil
}

// strictInput reads the per-invocation strict posture: the environment is
// the only source, so a PR can never make its own warnings fatal.
func strictInput(env map[string]string) bool {
	raw, ok := env["REVIEW_STRICT_INPUT"]
	return ok && strings.TrimSpace(raw) != "" && strings.TrimSpace(raw) == "1"
}

func (s *Snapshot) write(bundleDir string, contents map[string][]byte) *core.RefusalError {
	for _, path := range Inventory {
		raw, ok := contents[path]
		entry := core.ControlsFileEntry{Path: path, Status: statusMissing}
		if ok {
			entry = core.ControlsFileEntry{Path: path, Status: statusUsed, Bytes: int64(len(raw)), SHA256: hash(raw)}
			if ref := writeAt(filepath.Join(s.Dir, filepath.FromSlash(snapshotRel(path))), raw); ref != nil {
				return ref
			}
		}
		s.File.Files = append(s.File.Files, entry)
	}
	doc, err := json.MarshalIndent(s.File, "", "  ")
	if err != nil {
		return refuse(CodeUnavailable, fmt.Sprintf("could not encode %s: %v", FileName, err))
	}
	return writeAt(filepath.Join(bundleDir, FileName), append(doc, '\n'))
}

func writeAt(dest string, raw []byte) *core.RefusalError {
	err := os.MkdirAll(filepath.Dir(dest), dirPerm)
	if err == nil {
		err = atomic.WriteFile(dest, raw)
	}
	if err != nil {
		return refuse(CodeUnavailable, fmt.Sprintf("could not write %s: %v", dest, err))
	}
	return nil
}

// Gate loads a bundle's controls.json and verifies it against the bundle's
// base commit and the bytes on disk; drift is a typed refusal.
func Gate(bundleDir, bundleBase string) (*Snapshot, *core.RefusalError) {
	raw, err := jq.CappedFile(filepath.Join(bundleDir, FileName), MaxTotalBytes)
	s := &Snapshot{Dir: filepath.Join(bundleDir, DirName)}
	if err != nil || json.Unmarshal(raw, &s.File) != nil || s.File.SchemaVersion != schemaVersion ||
		(s.File.Policy != PolicyBase && s.File.Policy != PolicyHead) {
		return nil, refuse(CodeUnavailable, "bundle has no valid controls.json — re-fetch it (fetch resolves controls at the PR base)")
	}
	if ref := s.verify(bundleBase); ref != nil {
		return nil, ref
	}
	return s, nil
}

// GateRecorded loads a bundle's controls.json and re-hashes the snapshot
// against the base the file records. Read-only verbs (config preview,
// explanation) use it when they hold no bundle base of their own: the
// hashes still prove the captured bytes are unchanged.
func GateRecorded(bundleDir string) (*Snapshot, *core.RefusalError) {
	raw, err := jq.CappedFile(filepath.Join(bundleDir, FileName), MaxTotalBytes)
	s := &Snapshot{Dir: filepath.Join(bundleDir, DirName)}
	if err != nil || json.Unmarshal(raw, &s.File) != nil || s.File.SchemaVersion != schemaVersion ||
		(s.File.Policy != PolicyBase && s.File.Policy != PolicyHead) {
		return nil, refuse(CodeUnavailable, "bundle has no valid controls.json — re-fetch it (fetch resolves controls at the PR base)")
	}
	if ref := s.verify(s.File.BaseSHA); ref != nil {
		return nil, ref
	}
	return s, nil
}

// Entry finds one inventory row in the snapshot by its tracked path,
// including rows recorded missing.
func (s *Snapshot) Entry(rel string) *core.ControlsFileEntry {
	if s == nil {
		return nil
	}
	for i := range s.File.Files {
		if s.File.Files[i].Path == rel {
			return &s.File.Files[i]
		}
	}
	return nil
}

func (s *Snapshot) verify(bundleBase string) *core.RefusalError {
	f := s.File
	if bundleBase == "" || f.BaseSHA != bundleBase || (f.Policy == PolicyBase && f.SHA != bundleBase) {
		return refuse(CodeStale, fmt.Sprintf("controls base %q does not match the bundle base %q — re-fetch", f.BaseSHA, bundleBase))
	}
	for _, e := range f.Files {
		if s.drifted(e) {
			return refuse(CodeStale, fmt.Sprintf("controls snapshot file %s changed under the bundle — re-fetch the bundle", e.Path))
		}
	}
	return nil
}

func (s *Snapshot) drifted(e core.ControlsFileEntry) bool {
	body, ok := s.ReadFile(e.Path)
	return e.Status == statusUsed && (!ok || int64(len(body)) != e.Bytes || hash(body) != e.SHA256)
}

// Path maps an inventory path to its snapshot file, or "" when it is not a
// used control (so no other name can escape the snapshot directory).
func (s *Snapshot) Path(rel string) string {
	if s == nil || !slices.Contains(Inventory, rel) {
		return ""
	}
	for _, e := range s.File.Files {
		if e.Path == rel && e.Status == statusUsed {
			return filepath.Join(s.Dir, filepath.FromSlash(snapshotRel(rel)))
		}
	}
	return ""
}

// Has reports whether rel is a used control in this snapshot.
func (s *Snapshot) Has(rel string) bool { return s.Path(rel) != "" }

// ReadFile reads one used control from the snapshot, capped like its source.
func (s *Snapshot) ReadFile(rel string) ([]byte, bool) {
	path := s.Path(rel)
	if path == "" {
		return nil, false
	}
	raw, err := jq.CappedFile(path, MaxFileBytes)
	return raw, err == nil
}

// StrictInput reports the snapshot's strict_input control: warnings in the
// agent's review.json become errors, and record refuses when any remain.
func (s *Snapshot) StrictInput() bool { return s != nil && s.File.StrictInput }

// OverridesDir is the snapshot's overrides fragment directory.
func (s *Snapshot) OverridesDir() string { return filepath.Join(s.Dir, "overrides") }

// Used reports how many inventory files were materialized.
func (s *Snapshot) Used() int {
	n := 0
	for _, e := range s.File.Files {
		if e.Status == statusUsed {
			n++
		}
	}
	return n
}

func readWorktree(root string) (map[string][]byte, *core.RefusalError) {
	contents, total := map[string][]byte{}, 0
	for _, path := range Inventory {
		full := filepath.Join(root, path)
		if fi, err := os.Stat(full); err != nil || fi.IsDir() {
			continue // absent, or a directory squatting the name: no control
		}
		raw, err := jq.CappedFile(full, MaxFileBytes)
		if total += len(raw); err != nil || total > MaxTotalBytes {
			return nil, refuse(CodeTooLarge, "control "+path+" is unreadable or the controls are over their size caps")
		}
		contents[path] = raw
	}
	return contents, nil
}

func refuse(code, message string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: message}
}

func hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
