package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
//
// InputsSHA256 binds every captured input (everything below except
// Outcomes): an outcome append leaves it stable, while any edit of the
// captured inputs moves it. Inventory pins the trusted-base support
// bytes by path. RequiresEvidence marks bundles written by the typed
// fetch: readers enforce the binding, while older artifacts without it
// report unavailable provenance instead of claiming verified policy.
type RunState struct {
	Resolved         *Resolved          `json:"resolved"`
	SchemaVersion    string             `json:"schema_version"`
	Name             string             `json:"name"`
	BaseSHA          string             `json:"base_sha"`
	CapturedAt       string             `json:"captured_at"`
	ConfigSHA256     string             `json:"config_sha256"`
	InputsSHA256     string             `json:"inputs_sha256,omitempty"`
	Inventory        map[string]string  `json:"inventory,omitempty"`
	Evidence         Evidence           `json:"evidence"`
	Outcomes         []CheckObservation `json:"outcomes,omitempty"`
	Root             RequirementSet     `json:"root"`
	Trusted          bool               `json:"trusted"`
	RequiresEvidence bool               `json:"requires_evidence,omitempty"`
}

// StatusUsed marks a snapshot entry whose bytes were captured.
const StatusUsed = "used"

// runStateFileName is the one file inside the per-run state directory.
const runStateFileName = "run.json"

// runSchemaVersion pins the run-state file shape.
const runSchemaVersion = "1"

// WriteRunState persists the run bundle at dir/run.json, binding the
// captured inputs first: the digest covers every input, never the
// appended outcomes, so a later outcome append leaves it stable. The
// caller ensures the ignore-first layout; this write only creates the
// bundle directory itself.
func WriteRunState(dir string, rs *RunState) *stateError {
	rs.SchemaVersion = runSchemaVersion
	rs.Name = filepath.Base(dir)
	rs.RequiresEvidence = true
	rs.InputsSHA256 = inputsDigest(rs)
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return &stateError{msg: "could not encode run state for " + rs.Name, cause: err}
	}
	path := filepath.Join(dir, runStateFileName)
	if _, serr := confineBundleDir(dir); serr != nil {
		return serr
	}
	if err := os.MkdirAll(dir, stateDirPerm); err != nil {
		return &stateError{msg: "could not create run state directory " + rs.Name, cause: err}
	}
	if werr := atomic.WriteFile(path, append(raw, '\n')); werr != nil {
		return &stateError{msg: "could not write " + path, cause: werr}
	}
	return nil
}

// confineBundleDir proves the bundle directory stays under a repository
// state area: the path must resolve inside a `.verdict/state` parent, so
// a caller-supplied bundle path can never escape to an unrelated target.
func confineBundleDir(dir string) (string, *stateError) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", &stateError{msg: "could not resolve bundle directory " + dir, cause: err}
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err == nil {
		abs = filepath.Join(resolvedDir, filepath.Base(abs))
	}
	parent := filepath.Base(filepath.Dir(abs))
	grandparent := filepath.Base(filepath.Dir(filepath.Dir(abs)))
	if parent != StateDirName || grandparent != VerdictDir {
		outside := filepath.Join(VerdictDir, StateDirName)
		return "", &stateError{msg: "refuses to write run state outside " + outside + ": " + dir, operator: true}
	}
	return abs, nil
}

// ifEmpty defaults an empty root to the working directory, matching
// NewLayout.
func ifEmpty(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// ReadRunState loads the persisted run bundle at dir/run.json: absent
// reads (nil, false, nil) so legacy runs degrade to no repository policy
// instead of refusing. Present but malformed state refuses loudly: the
// operator re-fetches or removes it.
func ReadRunState(dir string) (*RunState, bool, *stateError) {
	path := filepath.Join(dir, runStateFileName)
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
		msg := path + " has schema_version " + rs.SchemaVersion
		return nil, false, &stateError{msg: msg + " (want 1; re-fetch to rebuild it)", operator: true}
	}
	return &rs, true, nil
}

// AppendOutcome records one observed check execution on an existing run
// bundle, preserving every captured input byte-for-byte. Absent run
// state appends nothing (false, nil): legacy runs carry no typed policy,
// and inventing one from an outcome would reconstruct inputs the fetch
// never captured. The error return is the sealed state error as an
// error so verb packages can warn without naming the type.
func AppendOutcome(dir string, obs *CheckObservation) (bool, error) {
	rs, found, serr := ReadRunState(dir)
	if serr != nil {
		return false, serr
	}
	if !found {
		return false, nil
	}
	rs.Outcomes = append(rs.Outcomes, *obs)
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return false, &stateError{msg: "could not encode run state for " + rs.Name, cause: err}
	}
	path := filepath.Join(dir, runStateFileName)
	if werr := atomic.WriteFile(path, append(raw, '\n')); werr != nil {
		return false, &stateError{msg: "could not write " + path, cause: werr}
	}
	return true, nil
}

// VerifyRunState loads the run bundle at dir/run.json and binds it to the
// controls snapshot beside it, refusing controls-stale on any drift: a
// deleted or corrupt bundle, edited captured inputs, or support bytes
// that no longer hash to the captured values. A bare bundle directory
// that predates typed evidence reads legacy (nil, nil): older artifacts
// report unavailable provenance instead of claiming verified policy. A
// bundle directory carrying the controls snapshot without its run bundle
// refuses: that pairing means deletion, not legacy.
// Callers gate record and post on the refusal and warn on unavailable
// provenance; Verified reports whether the full binding held.
func VerifyRunState(dir string) (*RunState, *core.RefusalError) {
	rs, found, serr := ReadRunState(dir)
	if serr != nil {
		return nil, stale(dir, "run bundle is unreadable")
	}
	if !found {
		return missingRunState(dir)
	}
	if rs.InputsSHA256 != "" && inputsDigest(rs) != rs.InputsSHA256 {
		return nil, stale(dir, "captured inputs changed under the bundle")
	}
	return bindRunState(dir, rs)
}

// missingRunState reads an absent run bundle: a bare directory predates
// typed evidence (nil, nil), while a directory carrying the controls
// snapshot alone means the bundle was deleted.
func missingRunState(dir string) (*RunState, *core.RefusalError) {
	if _, err := os.Stat(filepath.Join(dir, controls.FileName)); err == nil {
		return nil, stale(dir, "run bundle was deleted from its typed snapshot")
	}
	return nil, nil
}

// bindRunState re-hashes the controls snapshot and every captured hash
// the bundle pins: the base snapshot first, then the configuration and
// support bytes it captured.
func bindRunState(dir string, rs *RunState) (*RunState, *core.RefusalError) {
	snap, ref := controls.Gate(dir, rs.BaseSHA)
	if ref != nil {
		return nil, ref
	}
	if ref := verifyConfigBytes(rs, snap); ref != nil {
		return nil, ref
	}
	if ref := verifyInventory(dir, rs, snap); ref != nil {
		return nil, ref
	}
	return rs, nil
}

// Verified reports whether the loaded bundle carries the full integrity
// binding: bundles predating it enforce their stored policy but report
// unavailable provenance instead of claiming verified evidence.
func (rs *RunState) Verified() bool {
	return rs != nil && rs.RequiresEvidence && rs.InputsSHA256 != ""
}

// stale refuses with the snapshot-drift code: the typed evidence changed
// under the bundle, so no verb may proceed on it — re-fetch rebuilds it.
func stale(dir, why string) *core.RefusalError {
	return &core.RefusalError{Code: controls.CodeStale, Message: "typed evidence " + why +
		" (" + RunBundlePath(dir) + " changed under the bundle — re-fetch the bundle)"}
}

// verifyConfigBytes compares the captured configuration hash to the
// snapshot entry controls.Gate already re-hashed: persisting a hash never
// proves anything, recomputing it does. No captured policy means nothing
// to check; a missing snapshot entry under a captured policy is drift.
func verifyConfigBytes(rs *RunState, snap *controls.Snapshot) *core.RefusalError {
	if rs.ConfigSHA256 == "" {
		return nil
	}
	entry := snap.Entry(ConfigPath)
	if entry == nil || entry.SHA256 != rs.ConfigSHA256 {
		return stale(snap.Dir, "configuration snapshot no longer matches the captured hash")
	}
	return nil
}

// verifyInventory compares every support hash pinned at capture to the
// snapshot entry controls.Gate already re-hashed: a capture that pins a
// file the snapshot no longer carries refuses instead of silently
// degrading to no policy.
func verifyInventory(dir string, rs *RunState, snap *controls.Snapshot) *core.RefusalError {
	for _, path := range sortedKeys(rs.Inventory) {
		entry := snap.Entry(VerdictDir + "/" + path)
		if entry == nil || entry.SHA256 != rs.Inventory[path] {
			return stale(dir, "support snapshot "+path+" no longer matches the captured hash")
		}
	}
	return nil
}

// inputsDigest hashes the canonical captured inputs: every stored field
// except the mutable outcome ledger and the digest itself, so valid
// outcome appends never invalidate the captured input hash.
func inputsDigest(rs *RunState) string {
	cp := *rs
	cp.Outcomes = nil
	cp.InputsSHA256 = ""
	// The digest binds logical inputs, not volatile serialization:
	// capture timestamps and temp paths vary run to run for the same
	// policy, so they ride along as provenance but stay out of the
	// seal (values, origins, base, and labeled hashes carry it).
	cp.CapturedAt = ""
	cp.Evidence.CapturedAt = ""
	raw, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	return hashBytes(raw)
}

// sortedKeys returns the map keys in stable order.
func sortedKeys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for path := range m {
		names = append(names, path)
	}
	slices.Sort(names)
	return names
}

// InventoryHashes pins the trusted-base support bytes by snapshot path:
// the capture-time manifest the integrity check re-checks against the
// snapshot entries. Paths shed the tracked directory prefix, so
// "kb.jsonl" is the tracked ".verdict/kb.jsonl".
func InventoryHashes(snap *controls.Snapshot) map[string]string {
	out := map[string]string{}
	for _, entry := range snap.File.Files {
		if entry.Status != StatusUsed {
			continue
		}
		out[strings.TrimPrefix(entry.Path, VerdictDir+"/")] = entry.SHA256
	}
	return out
}

// hashBytes renders the lowercase hex SHA-256 of raw.
func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
