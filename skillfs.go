// Package skillfs embeds the Bootstrap skill from skills/verdict/, the
// Procedure from procedure/, and the published schemas from schemas/. Go embed
// cannot reach parent directories, so this root package embeds each tree in
// place and serves them through one read-only multi-root fs. Inventory paths
// keep their pre-split names: "prompts/review.md", "packs/…", "references/…",
// and "schemas/review.schema.json".
package skillfs

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// SkillMD is the stub entry point, relative to the payload root.
const SkillMD = "SKILL.md"

// ProcedureMD is the Procedure index, served as the default `verdict skill`
// body. It lives in the embedded payload, not in the installed stub.
const ProcedureMD = "PROCEDURE.md"

// StubVersion is the stub contract version served by this binary. The
// Bootstrap skill's frontmatter carries the same integer and `skill status`
// compares the installed value against it.
const StubVersion = "1"

// Dir is the checkout directory the skills CLI discovers and copies, so it
// carries only the three Bootstrap skill files.
const Dir = "skills/verdict"

// ProcedureDir is the checkout directory that holds the Procedure payload.
// Its files are served at the same inventory paths the skill used before the
// split, with PROCEDURE.md added.
const ProcedureDir = "procedure"

// SchemasDir is the checkout directory that holds the published schemas. It
// is embedded separately but served under the same name in the payload.
const SchemasDir = "schemas"

// stub is the installed Bootstrap skill: the shim, its notice, and the entry
// point. Patterns name files explicitly, so a stray file beside them is not
// embedded (and TestEmbedMatchesRepo fails on it).
//
//go:embed skills/verdict/SKILL.md skills/verdict/NOTICE.md skills/verdict/scripts/verdict
var stub embed.FS

// procedure is the task instruction payload: the Procedure index, prompts,
// built-in lens pack, and references. All of it is served by the binary and
// none of it is copied into an installed skill.
//
//go:embed procedure
var procedure embed.FS

// published is the repository's published schemas, served at "schemas/...".
//
//go:embed schemas
var published embed.FS

// FS serves the stub, the Procedure, and the published schemas under one root.
var FS = newFS()

const rootPath = "."

const (
	verbOpen = "open"
	verbRead = "read"
	verbStat = "stat"
)

type embeddedError struct {
	cause error
	verb  string
	name  string
}

func (e *embeddedError) Error() string { return e.verb + " " + e.name + ": " + e.cause.Error() }

func (e *embeddedError) Unwrap() error { return e.cause }

func wrapEmbedded(verb, name string, cause error) error {
	return &embeddedError{verb: verb, name: name, cause: cause}
}

// multiFS resolves one inventory path against each root in order. A missing
// file in one root is a normal miss; any other error is preserved.
type multiFS struct {
	roots []fs.FS
}

func newFS() fs.FS {
	stubRoot, err := fs.Sub(stub, Dir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	procedureRoot, err := fs.Sub(procedure, ProcedureDir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	return multiFS{roots: []fs.FS{stubRoot, procedureRoot, published}}
}

func (m multiFS) Open(name string) (fs.File, error) {
	if name == rootPath {
		return openRoot(m)
	}
	for _, root := range m.roots {
		file, err := root.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, wrapEmbedded(verbOpen, name, err)
		}
	}
	return nil, wrapEmbedded(verbOpen, name, fs.ErrNotExist)
}

// openRoot merges the roots' top-level entries into one sorted directory. The
// first root supplies the directory metadata; every root supplies entries.
func openRoot(m multiFS) (fs.File, error) {
	var info fs.FileInfo
	var entries []fs.DirEntry
	for i, root := range m.roots {
		got, rootInfo, err := readRootEntries(root, rootName(i))
		if err != nil {
			return nil, err
		}
		if i == 0 {
			info = rootInfo
		}
		entries = append(entries, got...)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return &rootDir{info: info, entries: entries}, nil
}

// HandoffFacts are the bundle facts that decide a conditional handoff section.
// A false fact appends no section — there is no stub, placeholder, or hint.
type HandoffFacts struct {
	Chunked      bool
	PriorThreads bool
	KB           bool
	Lenses       bool
	Context      bool
	// Spec is true when a context source carries role: spec, which turns the
	// handed spec into a checklist the review must answer.
	Spec bool
}

const (
	roleMethodMD       = "prompts/handoff/role-method.md"
	findingsContractMD = "prompts/handoff/findings-contract.md"
	repliesContractMD  = "prompts/handoff/replies-contract.md"
	capabilityMapMD    = "prompts/handoff/capability-map.md"
	chunkedSectionMD   = "prompts/handoff/chunked.md"
	threadsSectionMD   = "prompts/handoff/threads.md"
	kbSectionMD        = "prompts/handoff/kb.md"
	lensesSectionMD    = "prompts/handoff/lenses.md"
	contextSectionMD   = "prompts/handoff/context.md"
	specSectionMD      = "prompts/handoff/spec.md"
)

type handoffSection struct {
	has  func(HandoffFacts) bool
	name string
}

// handoffSectionOrder is the fixed order every assembly trusts. A section's
// position never depends on the order facts were set.
var handoffSectionOrder = []handoffSection{
	{name: chunkedSectionMD, has: func(f HandoffFacts) bool { return f.Chunked }},
	{name: threadsSectionMD, has: func(f HandoffFacts) bool { return f.PriorThreads }},
	{name: kbSectionMD, has: func(f HandoffFacts) bool { return f.KB }},
	{name: lensesSectionMD, has: func(f HandoffFacts) bool { return f.Lenses }},
	{name: contextSectionMD, has: func(f HandoffFacts) bool { return f.Context }},
	{name: specSectionMD, has: func(f HandoffFacts) bool { return f.Spec }},
}

// HandoffRef is one on-demand bundle reference. Path is the bundle slash
// path; Source is the embedded payload; From/To bound an optional byte slice.
type HandoffRef struct {
	Path   string
	Source string
	From   string
	To     string
}

// handoffRefs is the pull list. The capability map names these paths; the
// full operator workflow and body-format rules are not part of the handoff.
var handoffRefs = []HandoffRef{
	{Path: "refs/review-checklist.md", Source: "prompts/review.md", From: "## Checklist\n", To: "## Review body format"},
	{Path: "refs/system-boundaries.md", Source: "prompts/system.md"},
	{Path: "refs/diagrams.md", Source: "prompts/diagrams.md"},
	{Path: "refs/incremental-review.md", Source: "prompts/incremental-review.md"},
	{Path: "refs/reviewer-subagent.md", Source: "prompts/reviewer-subagent.md"},
	{Path: "refs/commands.md", Source: "references/commands.md"},
	{Path: "refs/panel.md", Source: "references/panel.md"},
	{Path: "refs/lens-pack.md", Source: "packs/builtin/pack.json"},
}

// HandoffRefs returns the pull list in capability-map order.
func HandoffRefs() []HandoffRef { return slices.Clone(handoffRefs) }

// ReadRef returns the exact bytes a pull ref writes. An empty From/To copies
// the whole source file; otherwise both markers must be present and the ref
// is the byte slice from the start of From up to the start of To.
func ReadRef(ref HandoffRef) ([]byte, error) {
	raw, err := Read(ref.Source)
	if err != nil {
		return nil, err
	}
	if ref.From == "" && ref.To == "" {
		return raw, nil
	}
	start, end, err := refBounds(raw, ref)
	if err != nil {
		return nil, err
	}
	return raw[start:end], nil
}

func refBounds(raw []byte, ref HandoffRef) (int, int, error) {
	start, end := 0, len(raw)
	if ref.From != "" {
		start = bytes.Index(raw, []byte(ref.From))
		if start < 0 {
			return 0, 0, &ProcedureFileError{
				Path:  ref.Source,
				Cause: jq.NewErrf("reference start marker %q is missing", ref.From),
			}
		}
	}
	if ref.To != "" {
		i := bytes.Index(raw[start:], []byte(ref.To))
		if i < 0 {
			return 0, 0, &ProcedureFileError{
				Path:  ref.Source,
				Cause: jq.NewErrf("reference end marker %q is missing", ref.To),
			}
		}
		end = start + i
	}
	return start, end, nil
}

// HandoffFiles lists every embedded file that feeds the handoff core, its
// sections, or its refs. Provenance hashes these alongside the six baseline
// prompt files, so an edit to any assembled byte moves prompts_hash.
func HandoffFiles() []string {
	files := []string{roleMethodMD, findingsContractMD, repliesContractMD, capabilityMapMD}
	for _, section := range handoffSectionOrder {
		files = append(files, section.name)
	}
	for _, ref := range handoffRefs {
		files = append(files, ref.Source)
	}
	slices.Sort(files)
	return files
}

// WriteBundleRefs writes the pull refs into a bundle's refs directory. The
// bytes come only from this binary's embedded payload, never the checkout.
func WriteBundleRefs(dir string) error {
	refsDir := filepath.Join(dir, "refs")
	if err := os.MkdirAll(refsDir, execMode); err != nil {
		return jq.Sealed("create bundle refs dir", err)
	}
	for _, ref := range handoffRefs {
		raw, err := ReadRef(ref)
		if err != nil {
			return err
		}
		name, _ := strings.CutPrefix(ref.Path, "refs/")
		path := filepath.Join(refsDir, filepath.FromSlash(name))
		if err := atomic.WriteFile(path, raw); err != nil {
			return jq.Sealed("write bundle ref "+filepath.ToSlash(path), err)
		}
	}
	return nil
}

// readRootEntries opens one overlay root and drains its directory, closing
// the handle before the merged root returns it to the caller.
func readRootEntries(root fs.FS, name string) ([]fs.DirEntry, fs.FileInfo, error) {
	dir, err := root.Open(rootPath)
	if err != nil {
		return nil, nil, wrapEmbedded(verbOpen, name, err)
	}
	info, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, nil, wrapEmbedded(verbStat, name, err)
	}
	readDir, ok := dir.(fs.ReadDirFile)
	if !ok {
		_ = dir.Close()
		return nil, nil, wrapEmbedded(verbOpen, name+" is not a directory", fs.ErrInvalid)
	}
	entries, err := readDir.ReadDir(-1)
	_ = dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, wrapEmbedded(verbRead, name, err)
	}
	return entries, info, nil
}

func rootName(i int) string {
	switch i {
	case 0:
		return Dir
	case 1:
		return ProcedureDir
	default:
		return SchemasDir
	}
}

type rootDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
}

func (d *rootDir) Stat() (fs.FileInfo, error) { return d.info, nil }

func (d *rootDir) Read([]byte) (int, error) { return 0, io.EOF }

func (d *rootDir) Close() error { return nil }

func (d *rootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if len(d.entries) == 0 {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 || n >= len(d.entries) {
		out := d.entries
		d.entries = nil
		return out, nil
	}
	out := d.entries[:n]
	d.entries = d.entries[n:]
	return out, nil
}

const (
	execMode = 0o755
	fileMode = 0o644
)

var executable = map[string]bool{
	"schemas/validate.sh": true,
	"scripts/verdict":     true,
}

// Paths lists every embedded payload path in slash form, sorted.
func Paths() ([]string, error) {
	var out []string
	err := fs.WalkDir(FS, rootPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("walk embedded payload: %v", err)
	}
	slices.Sort(out)
	return out, nil
}

// ProcedureFileError names the embedded payload file whose read failed. A
// caller that must refuse on a missing preamble inspects Path with [errors.As].
type ProcedureFileError struct {
	Cause error
	Path  string
}

// Error reports the embedded path and its read failure.
func (e *ProcedureFileError) Error() string {
	return "read embedded " + e.Path + ": " + e.Cause.Error()
}

// Unwrap exposes the underlying read failure.
func (e *ProcedureFileError) Unwrap() error { return e.Cause }

const systemPromptMD = "prompts/system.md"

// reviewCoreFiles is the review core's trust order, in one place.
func reviewCoreFiles() []string {
	return []string{roleMethodMD, findingsContractMD, capabilityMapMD}
}

// drainProcedureFiles is the drain core's trust order: the same role, method,
// and map, with the replies artifact contract in place of findings.
func drainCoreFiles() []string {
	return []string{roleMethodMD, repliesContractMD, capabilityMapMD}
}

// taskFiles is the single task vocabulary: the two handoff tasks assemble a
// lean core, and the remaining operator tasks keep their task prompt.
var taskFiles = map[string]func() []string{
	"address-reviews":    drainCoreFiles,
	"fix-ci":             func() []string { return genericTaskFiles("fix-ci") },
	"incremental-review": func() []string { return genericTaskFiles("incremental-review") },
	"review":             reviewCoreFiles,
	"validate":           func() []string { return genericTaskFiles("validate") },
}

func genericTaskFiles(task string) []string {
	return []string{ProcedureMD, systemPromptMD, "prompts/" + task + ".md"}
}

// Tasks lists the Procedure tasks this binary serves, sorted by name.
func Tasks() []string { return slices.Sorted(maps.Keys(taskFiles)) }

// UnknownTaskError names a task this binary does not serve. Callers inspect it
// with [errors.As].
type UnknownTaskError struct {
	Task string
}

// Error reports the unknown task name.
func (e *UnknownTaskError) Error() string { return "unknown task: " + e.Task }

// TaskProcedure returns one task's Procedure, assembled from the embedded
// payload in the same trust order the bundle uses. It never reads the
// filesystem. An unknown task returns no bytes and an [UnknownTaskError].
func TaskProcedure(task string) ([]byte, error) {
	files, ok := taskFiles[task]
	if !ok {
		return nil, &UnknownTaskError{Task: task}
	}
	if task == "review" || task == "address-reviews" {
		return assembleHandoffFiles(files())
	}
	return assembleProcedure(files())
}

// assembleProcedure concatenates the named embedded files in order. A missing
// file yields no bytes and a ProcedureFileError naming the path: the whole
// assembly is refused, never published partially.
func assembleProcedure(files []string) ([]byte, error) {
	var out []byte
	for _, name := range files {
		raw, err := Read(name)
		if err != nil {
			return nil, &ProcedureFileError{Path: name, Cause: err}
		}
		out = append(out, raw...)
	}
	return out, nil
}

// assembleHandoffFiles leaves one blank line between embedded files, so every
// appended ## heading has a blank line before it without a trailing blank
// line in the formatted source payload.
func assembleHandoffFiles(files []string) ([]byte, error) {
	var out []byte
	for i, name := range files {
		if i > 0 {
			out = append(out, '\n')
		}
		raw, err := Read(name)
		if err != nil {
			return nil, &ProcedureFileError{Path: name, Cause: err}
		}
		out = append(out, raw...)
	}
	return out, nil
}

// ReviewCore returns the pushed review handoff: role, method, findings
// contract, and capability map. It is the ≤3 KB core an archtest pins; the
// operator workflow and body-format rules are pull refs, not pushed bytes.
func ReviewCore() ([]byte, error) {
	return assembleHandoffFiles(reviewCoreFiles())
}

// ReviewProcedureFor assembles the lean review core plus the sections whose
// facts are true, in handoffSectionOrder. A false fact contributes no bytes.
func ReviewProcedureFor(facts HandoffFacts) ([]byte, error) {
	return assembleHandoff(reviewCoreFiles(), facts, true)
}

// DrainProcedureFor assembles the drain core plus the prior-threads section,
// the only conditional section a drain handoff uses.
func DrainProcedureFor(facts HandoffFacts) ([]byte, error) {
	return assembleHandoff(drainCoreFiles(), facts, false)
}

// sectionApplies gates sections by bundle fact and handoff kind: drain keeps
// only prior threads, because it answers threads rather than reading a diff.
func sectionApplies(section handoffSection, facts HandoffFacts, review bool) bool {
	if !review && section.name != threadsSectionMD {
		return false
	}
	return section.has(facts)
}

// assembleHandoff appends the sections whose facts are true, each separated
// from the preceding bytes by one blank line.
func assembleHandoff(core []string, facts HandoffFacts, review bool) ([]byte, error) {
	raw, err := assembleHandoffFiles(core)
	if err != nil {
		return nil, err
	}
	for _, section := range handoffSectionOrder {
		if !sectionApplies(section, facts, review) {
			continue
		}
		part, err := Read(section.name)
		if err != nil {
			return nil, &ProcedureFileError{Path: section.name, Cause: err}
		}
		raw = append(raw, '\n')
		raw = append(raw, part...)
	}
	return raw, nil
}

// ReviewProcedure returns the review handoff with no bundle facts: the pushed
// core alone. Bundle-aware callers use ReviewProcedureFor; MCP prompts and the
// golden harness use this fact-free public contract. A missing file returns no
// bytes and a ProcedureFileError.
func ReviewProcedure() ([]byte, error) {
	return ReviewProcedureFor(HandoffFacts{})
}

// DrainProcedure returns the drain handoff with no bundle facts: the pushed
// drain core alone. The drain bundle writer may pass facts through
// DrainProcedureFor; MCP prompts use this fact-free contract.
func DrainProcedure() ([]byte, error) {
	return DrainProcedureFor(HandoffFacts{})
}

// Read returns one embedded payload file by its slash path.
func Read(name string) ([]byte, error) {
	raw, err := fs.ReadFile(FS, name)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("read embedded %s: %v", name, err)
	}
	return raw, nil
}

// Mode is the mode a payload file installs with: 0755 for the executable
// shim and schema validator, 0644 for everything else.
func Mode(name string) fs.FileMode {
	if executable[name] {
		return execMode
	}
	return fileMode
}
