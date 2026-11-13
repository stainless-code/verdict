// Package pack loads a lens pack: a strict JSON manifest plus one markdown
// body per lens and rule, hashed as one canonical tree. The built-in pack
// ships in skills/verdict/packs/builtin; org and repo packs layer above it.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// Refusal codes a malformed, oversized, conflicting, or escaping pack
// refuses with.
const (
	CodeBadPack        = "bad-pack"
	CodePackConflict   = "pack-conflict"
	CodePackTooLarge   = "pack-too-large"
	CodePackUnsafePath = "pack-unsafe-path"
)

// BuiltinRoot is the built-in pack's directory in the shipped skill payload.
const BuiltinRoot = "packs/builtin"

// Fixed layout names and caps. The caps are checked while walking, before
// any manifest or body is parsed.
const (
	ManifestFile  = "pack.json"
	LensesDir     = "lenses"
	RulesDir      = "rules"
	SchemaVersion = "1"

	MaxFileBytes = 64 * 1024
	MaxPackBytes = 1 << 20
	MaxFiles     = 256
	MaxLenses    = 64
	MaxDepth     = 8
)

// Rule severity values, the closed set the pack severity map resolves to.
const (
	SeverityCritical      = "critical"
	SeverityImportant     = "important"
	SeverityInformational = "informational"
)

const (
	examplesDir = "examples"
	evalsDir    = "evals"
	rootSelf    = "."
	pathSep     = "/"

	namePattern    = `^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`
	versionPattern = `^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`
	lensIDPattern  = `^[a-z0-9][a-z0-9-]*$`
	ruleIDPattern  = `^[a-z0-9][a-z0-9-]{0,64}$`

	hash8Len        = 8
	maxLensFiles    = 24
	maxLensFindings = 20
	mdExt           = ".md"
)

var (
	nameRE    = regexp.MustCompile(namePattern)
	versionRE = regexp.MustCompile(versionPattern)
	lensIDRE  = regexp.MustCompile(lensIDPattern)
	ruleIDRE  = regexp.MustCompile(ruleIDPattern)
)

var (
	errFileTooLarge = errors.New("file over its cap")
	errNotRegular   = errors.New("not a regular file")
)

// Manifest is the published pack.json shape.
type Manifest struct {
	SchemaVersion string            `const:"1"                 json:"schema_version"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Replace       []string          `json:"replace,omitempty"`
	Severity      map[string]string `json:"severity,omitempty" value_enum:"critical|important|informational"`
	Lenses        []LensSpec        `json:"lenses"             minItems:"1"`
	Rules         []RuleSpec        `json:"rules,omitempty"`
}

// LensSpec is one manifest lens row. Globs absent means the lens is offered
// for every plan group; max_files and max_findings are the dispatch caps the
// reviewer-subagent contract states.
type LensSpec struct {
	ID          string   `json:"id"`
	Globs       []string `json:"globs,omitempty"`
	MaxFiles    int      `json:"max_files,omitempty"    max:"24" min:"1"`
	MaxFindings int      `json:"max_findings,omitempty" max:"20" min:"1"`
}

// RuleSpec is one manifest rule row: a KB id, the severity-map key that
// resolves its level, the scope globs that decide when it applies, and the
// tools it talks about.
type RuleSpec struct {
	Category    string   `json:"category,omitempty"`
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	ID          string   `json:"id"`
	Severity    string   `json:"severity"        minLength:"1"`
	Globs       []string `json:"globs"           minItems:"1"`
	Tools       []string `json:"tools,omitempty"`
}

// Lens is one loaded lens: its manifest row plus the body and its eight-hex
// body digest. Pack and PackSlug identify the pack that won the id when a
// layered set merges.
//
//nolint:govet // the manifest row stays first so the published shape reads top-down
type Lens struct {
	LensSpec
	Body        []byte
	Hash8       string
	Pack        string
	PackSlug    string
	PackVersion string
}

// Rule is one loaded rule: its manifest row, the resolved severity level,
// the body, and its eight-hex body digest.
//
//nolint:govet // the manifest row stays first so the published shape reads top-down
type Rule struct {
	RuleSpec
	Level       string
	Body        []byte
	Hash8       string
	Pack        string
	PackSlug    string
	PackVersion string
}

// Pack is one loaded lens pack: its manifest, lenses and rules sorted by id,
// and the sha256 of the canonical loaded tree.
type Pack struct {
	Manifest
	Lenses []Lens
	Rules  []Rule
	Hash   string
	files  []packFile
}

// packFile is one evals/ or examples/ file: reserved payload carried through
// vendoring and folded into the tree hash but never parsed as a body.
type packFile struct {
	path string
	body []byte
}

// PayloadFile is one reserved evals/ or examples/ file carried by a pack.
type PayloadFile struct {
	Path string
	Body []byte
}

// PayloadFiles returns a sorted copy of the pack's reserved payload files.
func (p *Pack) PayloadFiles() []PayloadFile {
	if p == nil {
		return nil
	}
	files := make([]PayloadFile, len(p.files))
	for i, f := range p.files {
		files[i] = PayloadFile{Path: f.path, Body: append([]byte(nil), f.body...)}
	}
	slices.SortFunc(files, func(a, b PayloadFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

// Load reads the pack rooted at root. A missing pack returns (nil, nil): the
// caller degrades without one. Every other failure is typed.
//
//nolint:revive // the loader reads as one ordered walk-parse-load pipeline
func Load(fsys fs.FS, root string) (*Pack, *core.RefusalError) {
	if !confinedRoot(root) {
		return nil, unsafePath(root)
	}
	files, ref := walk(fsys, root)
	if ref != nil {
		return nil, ref
	}
	if len(files) == 0 {
		return nil, nil
	}
	if _, ok := files[ManifestFile]; !ok {
		return nil, badPack(fmt.Sprintf("%s carries no %s", root, ManifestFile))
	}
	raw, ref := readFile(fsys, path.Join(root, ManifestFile), ManifestFile)
	if ref != nil {
		return nil, ref
	}
	manifest, ref := parseManifest(raw, path.Join(root, ManifestFile))
	if ref != nil {
		return nil, ref
	}
	lenses, ref := loadLenses(fsys, root, files, manifest)
	if ref != nil {
		return nil, ref
	}
	rules, ref := loadRules(fsys, root, files, manifest)
	if ref != nil {
		return nil, ref
	}
	extras, ref := loadExtras(fsys, root, files)
	if ref != nil {
		return nil, ref
	}
	return &Pack{
		Manifest: *manifest, Lenses: lenses, Rules: rules,
		Hash: treeHash(raw, lenses, rules, extras), files: extras,
	}, nil
}

// LoadAt reads a pack under base, refusing a root that could escape it and
// confining every read through [os.Root]. A missing base returns (nil, nil).
// The pack root itself is Lstat'ed: a symlink or non-directory is refused,
// never followed to a tree outside the operator's choice.
func LoadAt(base, root string) (*Pack, *core.RefusalError) {
	return loadAt(base, root, root)
}

// loadAt is LoadAt with the refusal label: a caller reading a directory by its
// path names that path instead of the relative root it walks from.
//
//nolint:revive // the root checks read as one ordered gate before the walk
func loadAt(base, root, label string) (*Pack, *core.RefusalError) {
	if !confinedRoot(root) {
		return nil, unsafePath(label)
	}
	full := root
	if root != rootSelf {
		full = filepath.Join(base, filepath.FromSlash(root))
	} else if base != "" {
		full = base
	}
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, badPack(fmt.Sprintf("inspect pack root %s: %v", label, err))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, unsafePath(label)
	}
	dir, err := os.OpenRoot(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, badPack(fmt.Sprintf("open pack root %s: %v", base, err))
	}
	defer func() { _ = dir.Close() }()
	return Load(dir.FS(), root)
}

// LoadDir reads a pack whose root is dir itself.
func LoadDir(dir string) (*Pack, *core.RefusalError) {
	return loadAt(dir, rootSelf, dir)
}

// CheckOrgEntry refuses a settings packs[] entry that resolves inside the repo
// layer's tracked tree: fetch reads that tree from the PR base, so treating a
// path in it as the org layer would read PR-editable files at head.
func CheckOrgEntry(root, listed string) *core.RefusalError {
	base, err := filepath.Abs(root)
	if err != nil {
		return unresolvableOrg(listed, err)
	}
	full := listed
	if !filepath.IsAbs(full) {
		full = filepath.Join(base, filepath.FromSlash(listed))
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return unresolvableOrg(listed, err)
	}
	packs := filepath.Join(base, filepath.FromSlash(RepoPacksDir))
	rel, relErr := filepath.Rel(packs, abs)
	if insideRel(rel, relErr) || resolvesInside(packs, abs) {
		return badPack(fmt.Sprintf(
			"org pack path %q is inside %s, which is the repo layer read from the PR base — move it outside that tree",
			listed, RepoPacksDir))
	}
	return nil
}

// insideRel reports whether rel places a path inside its base. ".." and any
// "..<separator>" prefix are outside, and a Rel error is a Windows volume
// mismatch, which likewise proves the path is outside.
func insideRel(rel string, err error) bool {
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvesInside reports whether target lands inside dir after resolving both
// sides' symlinks. A side that does not exist proves nothing, so the lexical
// check stands alone there.
func resolvesInside(dir, target string) bool {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realDir, realTarget)
	return insideRel(rel, err)
}

// unresolvableOrg refuses an org entry whose path cannot be resolved, rather
// than silently treating it as outside the repo layer.
func unresolvableOrg(listed string, err error) *core.RefusalError {
	return badPack(fmt.Sprintf("org pack path %q cannot be resolved: %v", listed, err))
}

// walk lists the pack tree, checking the closed layout, every entry's
// regular-file nature, and every byte cap before any file is parsed.
func walk(fsys fs.FS, root string) (map[string]int, *core.RefusalError) {
	counter := &treeCounter{files: map[string]int{}}
	err := fs.WalkDir(fsys, root, func(name string, entry fs.DirEntry, err error) error {
		return counter.visit(root, name, entry, err)
	})
	if err != nil {
		if ref, ok := errors.AsType[*core.RefusalError](err); ok {
			return nil, ref
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, badPack(fmt.Sprintf("walk %s: %v", root, err))
	}
	return counter.files, nil
}

// treeCounter accumulates the walk's file map and caps.
type treeCounter struct {
	files map[string]int
	count int
	total int
}

// visit classifies one walked entry and applies the caps to a file.
func (c *treeCounter) visit(root, name string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	info, ierr := entry.Info()
	if ierr != nil {
		return badPack("inspect " + name + ": " + ierr.Error())
	}
	mode := info.Mode()
	if mode&fs.ModeSymlink != 0 || mode&fs.ModeIrregular != 0 {
		return unsafePath(name)
	}
	rel := relative(root, name)
	if strings.Count(rel, pathSep) > MaxDepth {
		return tooLarge(fmt.Sprintf("%s nests deeper than %d directories", name, MaxDepth))
	}
	if entry.IsDir() {
		return checkLayoutDir(name, rel)
	}
	if !mode.IsRegular() {
		return unsafePath(name)
	}
	return c.addFile(name, rel, info)
}

// checkLayoutDir refuses a directory outside the fixed layout. examples/ and
// evals/ may nest; lenses/ and rules/ stay flat because their stems are ids.
func checkLayoutDir(name, rel string) error {
	if rel == rootSelf || layoutDir(rel) || reservedDir(rel) {
		return nil
	}
	return badPack(name + " is outside the closed pack layout")
}

// reservedDir reports whether rel sits under the nested examples/ or evals/
// payload trees.
func reservedDir(rel string) bool {
	return strings.HasPrefix(rel, examplesDir+pathSep) || strings.HasPrefix(rel, evalsDir+pathSep)
}

// addFile checks one file's layout and size, then records it.
func (c *treeCounter) addFile(name, rel string, info fs.FileInfo) error {
	if !layoutFile(rel) {
		return badPack(name + " is outside the closed pack layout")
	}
	size := int(info.Size())
	if size > MaxFileBytes {
		return tooLarge(fmt.Sprintf("%s is %d bytes, over the %d-byte per-file cap", name, size, MaxFileBytes))
	}
	c.count++
	c.total += size
	if c.count > MaxFiles {
		return tooLarge(fmt.Sprintf("the pack has more than %d files", MaxFiles))
	}
	if c.total > MaxPackBytes {
		return tooLarge(fmt.Sprintf("the pack is over the %d-byte total cap", MaxPackBytes))
	}
	c.files[rel] = size
	return nil
}

// relative renders one walked path relative to root without a leading "./".
func relative(root, name string) string {
	if root == rootSelf {
		return name
	}
	if name == root {
		return rootSelf
	}
	return strings.TrimPrefix(name, root+pathSep)
}

// layoutDir reports whether a top-level directory is part of the fixed
// layout. examples/ and evals/ are carried through vendoring and the tree
// hash, but no loader parses their bodies.
func layoutDir(rel string) bool {
	switch rel {
	case LensesDir, RulesDir, examplesDir, evalsDir:
		return true
	default:
		return false
	}
}

// layoutFile reports whether one walked file is part of the fixed layout.
func layoutFile(rel string) bool {
	if rel == ManifestFile {
		return true
	}
	for _, dir := range []string{LensesDir, RulesDir, examplesDir, evalsDir} {
		if strings.HasPrefix(rel, dir+pathSep) {
			return true
		}
	}
	return false
}

// readFile reads one named pack file through a MaxFileBytes+1 bound: a file
// that grew after the walk is refused instead of read without limit, and a
// non-regular handle is refused before any body is consumed.
func readFile(fsys fs.FS, name, label string) ([]byte, *core.RefusalError) {
	body, err := readCapped(fsys, name)
	if err == nil {
		return body, nil
	}
	if errors.Is(err, errFileTooLarge) {
		return nil, tooLarge(fmt.Sprintf("%s is over the %d-byte per-file cap", label, MaxFileBytes))
	}
	if errors.Is(err, errNotRegular) {
		return nil, unsafePath(label)
	}
	return nil, badPack(fmt.Sprintf("read %s: %v", label, err))
}

// readCapped opens name and reads at most MaxFileBytes+1 bytes.
//
//nolint:wrapcheck // the caller classifies the sentinel and seals the message
func readCapped(fsys fs.FS, name string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if info, serr := f.Stat(); serr == nil && !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	body, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller classifies and seals it
	}
	if len(body) > MaxFileBytes {
		return nil, errFileTooLarge
	}
	return body, nil
}

// parseManifest decodes pack.json strictly and validates the semantic rules
// the published schema cannot express.
func parseManifest(raw []byte, name string) (*Manifest, *core.RefusalError) {
	if ref := checkManifestKeys(raw, name); ref != nil {
		return nil, ref
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, badPack(fmt.Sprintf("%s is not a pack manifest: %v", name, err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, badPack(name + " carries trailing data after the manifest object")
	}
	if ref := validate(&manifest, lensRows(raw), name); ref != nil {
		return nil, ref
	}
	return &manifest, nil
}

// checkManifestKeys refuses a key spelled in any other case: encoding/json
// matches field names case-insensitively, so only a raw key scan can enforce
// the exact lowercase spellings.
//
//nolint:revive // the raw key scan is one ordered top/lens/rule pass
func checkManifestKeys(raw []byte, name string) *core.RefusalError {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		//nolint:nilerr // the strict decoder owns the syntax refusal
		return nil
	}
	if ref := checkExactKeys(top, []string{
		"schema_version", "name", "version", "replace", "severity", "lenses", "rules",
	}, name); ref != nil {
		return ref
	}
	var shape struct {
		Lenses []map[string]json.RawMessage `json:"lenses"`
		Rules  []map[string]json.RawMessage `json:"rules"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		//nolint:nilerr // the strict decoder owns the syntax refusal
		return nil
	}
	for _, row := range shape.Lenses {
		if ref := checkExactKeys(row, []string{"id", "globs", "max_files", "max_findings"}, name); ref != nil {
			return ref
		}
	}
	for _, row := range shape.Rules {
		if ref := checkExactKeys(row, []string{"id", "severity", "globs", "tools", "category", "description", "examples"}, name); ref != nil {
			return ref
		}
	}
	return nil
}

// checkExactKeys refuses any key outside want, including a differently-cased
// spelling of one in it.
func checkExactKeys(obj map[string]json.RawMessage, want []string, name string) *core.RefusalError {
	for key := range obj {
		if !slices.Contains(want, key) {
			return badPack(fmt.Sprintf("%s carries key %q; manifest keys are exactly %s",
				name, key, strings.Join(want, ", ")))
		}
	}
	return nil
}

// lensRows returns the keys each manifest row spelled, so an optional cap set
// to zero refuses against the published minimum instead of reading as absent.
func lensRows(raw []byte) []map[string]json.RawMessage {
	var shape struct {
		Lenses []map[string]json.RawMessage `json:"lenses"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil
	}
	return shape.Lenses
}

// validate enforces the manifest rules pack.schema.json documents.
//
//nolint:revive // the manifest checks run in one fixed order for stable refusals
func validate(manifest *Manifest, lensRowSet []map[string]json.RawMessage, name string) *core.RefusalError {
	if ref := validateHeader(manifest, name); ref != nil {
		return ref
	}
	if ref := validateReplace(manifest, name); ref != nil {
		return ref
	}
	if ref := validateSeverity(manifest, name); ref != nil {
		return ref
	}
	if len(manifest.Lenses) == 0 {
		return badPack(name + " names no lenses")
	}
	if len(manifest.Lenses) > MaxLenses {
		return tooLarge(fmt.Sprintf("the pack names %d lenses, over the %d-lens cap",
			len(manifest.Lenses), MaxLenses))
	}
	seen := map[string]bool{}
	for i := range manifest.Lenses {
		if ref := validateLens(&manifest.Lenses[i], lensRow(lensRowSet, i), name, seen); ref != nil {
			return ref
		}
	}
	if ref := validateRules(manifest, name); ref != nil {
		return ref
	}
	return nil
}

// lensRow returns the spelled keys for row i, or nil when the shape scan
// cannot reach it.
func lensRow(rows []map[string]json.RawMessage, i int) map[string]json.RawMessage {
	if i < len(rows) {
		return rows[i]
	}
	return nil
}

// validateHeader checks the manifest version, identity, and semver.
func validateHeader(manifest *Manifest, name string) *core.RefusalError {
	if manifest.SchemaVersion != SchemaVersion {
		return badPack(fmt.Sprintf("%s schema_version is %q, want %q", name, manifest.SchemaVersion, SchemaVersion))
	}
	if !nameRE.MatchString(manifest.Name) {
		return badPack(fmt.Sprintf("%s name %q is not <namespace>/<slug>", name, manifest.Name))
	}
	if !versionRE.MatchString(manifest.Version) {
		return badPack(fmt.Sprintf("%s version %q is not semver", name, manifest.Version))
	}
	return nil
}

// validateReplace checks every declared replacement is a pack name and names
// no pack twice.
func validateReplace(manifest *Manifest, name string) *core.RefusalError {
	seen := map[string]bool{}
	for _, replaced := range manifest.Replace {
		if !nameRE.MatchString(replaced) {
			return badPack(fmt.Sprintf("%s replace entry %q is not <namespace>/<slug>", name, replaced))
		}
		if seen[replaced] {
			return badPack(fmt.Sprintf("%s replaces pack %q twice", name, replaced))
		}
		seen[replaced] = true
	}
	return nil
}

// validateSeverity checks the pack's severity map: every key must be
// non-empty and every value one of the three levels.
func validateSeverity(manifest *Manifest, name string) *core.RefusalError {
	for key, level := range manifest.Severity {
		if strings.TrimSpace(key) == "" {
			return badPack(name + " severity map carries an empty key")
		}
		if !validSeverity(level) {
			return badPack(fmt.Sprintf("%s severity %q is %q, want %s|%s|%s",
				name, key, level, SeverityCritical, SeverityImportant, SeverityInformational))
		}
	}
	return nil
}

// validSeverity reports whether level is one of the published values.
func validSeverity(level string) bool {
	switch level {
	case SeverityCritical, SeverityImportant, SeverityInformational:
		return true
	default:
		return false
	}
}

// validateLens checks one row's id, uniqueness, caps, and globs.
func validateLens(lens *LensSpec, row map[string]json.RawMessage, name string, seen map[string]bool) *core.RefusalError {
	if !lensIDRE.MatchString(lens.ID) {
		return badPack(fmt.Sprintf("%s lens id %q is not a lowercase slug", name, lens.ID))
	}
	if seen[lens.ID] {
		return badPack(fmt.Sprintf("%s repeats lens id %q", name, lens.ID))
	}
	seen[lens.ID] = true
	if ref := validateLensCaps(lens, row, name); ref != nil {
		return ref
	}
	return validateLensGlobs(lens, name)
}

// validateLensCaps checks the optional dispatch caps; a spelled zero is out of
// bounds even though an absent key decodes to the same zero.
func validateLensCaps(lens *LensSpec, row map[string]json.RawMessage, name string) *core.RefusalError {
	if _, ok := row["max_files"]; ok && (lens.MaxFiles < 1 || lens.MaxFiles > maxLensFiles) {
		return badPack(fmt.Sprintf("%s lens %q max_files must be 1..%d", name, lens.ID, maxLensFiles))
	}
	if _, ok := row["max_findings"]; ok && (lens.MaxFindings < 1 || lens.MaxFindings > maxLensFindings) {
		return badPack(fmt.Sprintf("%s lens %q max_findings must be 1..%d", name, lens.ID, maxLensFindings))
	}
	return nil
}

// validateLensGlobs refuses an empty glob rather than silently matching
// nothing.
func validateLensGlobs(lens *LensSpec, name string) *core.RefusalError {
	for _, glob := range lens.Globs {
		if strings.TrimSpace(glob) == "" {
			return badPack(fmt.Sprintf("%s lens %q carries an empty glob", name, lens.ID))
		}
	}
	return nil
}

// validateRules checks every rule row's id, uniqueness, severity reference,
// required globs, and tools.
//
//nolint:revive // one refusal per rule field reads in declaration order
func validateRules(manifest *Manifest, name string) *core.RefusalError {
	seen := map[string]bool{}
	for i := range manifest.Rules {
		rule := &manifest.Rules[i]
		if !ruleIDRE.MatchString(rule.ID) {
			return badPack(fmt.Sprintf("%s rule id %q is not a KB id", name, rule.ID))
		}
		if seen[rule.ID] {
			return badPack(fmt.Sprintf("%s repeats rule id %q", name, rule.ID))
		}
		seen[rule.ID] = true
		if len(rule.Globs) == 0 {
			return badPack(fmt.Sprintf("%s rule %q names no globs", name, rule.ID))
		}
		if _, ok := manifest.Severity[rule.Severity]; !ok {
			return badPack(fmt.Sprintf("%s rule %q severity %q is not in the severity map", name, rule.ID, rule.Severity))
		}
		if ref := validateRuleGlobs(rule, name); ref != nil {
			return ref
		}
		if ref := validateRuleTools(rule, name); ref != nil {
			return ref
		}
	}
	return nil
}

// validateRuleGlobs refuses an empty glob rather than a rule that never fires.
func validateRuleGlobs(rule *RuleSpec, name string) *core.RefusalError {
	for _, glob := range rule.Globs {
		if strings.TrimSpace(glob) == "" {
			return badPack(fmt.Sprintf("%s rule %q carries an empty glob", name, rule.ID))
		}
	}
	return nil
}

// validateRuleTools refuses an empty tool name.
func validateRuleTools(rule *RuleSpec, name string) *core.RefusalError {
	for _, tool := range rule.Tools {
		if strings.TrimSpace(tool) == "" {
			return badPack(fmt.Sprintf("%s rule %q carries an empty tool name", name, rule.ID))
		}
	}
	return nil
}

// loadLenses reads each manifest row's body, sorted by id. A lenses/ file no
// row names is counted but never loaded.
func loadLenses(fsys fs.FS, root string, files map[string]int, manifest *Manifest) ([]Lens, *core.RefusalError) {
	lenses := make([]Lens, 0, len(manifest.Lenses))
	for i := range manifest.Lenses {
		spec := manifest.Lenses[i]
		rel := path.Join(LensesDir, spec.ID+mdExt)
		if _, ok := files[rel]; !ok {
			return nil, missingBody(files, LensesDir, "lens", spec.ID)
		}
		body, ref := readFile(fsys, path.Join(root, rel), rel)
		if ref != nil {
			return nil, ref
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, badPack(fmt.Sprintf("lens %q has an empty body at %s", spec.ID, rel))
		}
		sum := sha256.Sum256(body)
		lenses = append(lenses, Lens{
			LensSpec: spec, Body: body, Hash8: hex.EncodeToString(sum[:])[:hash8Len],
		})
	}
	slices.SortFunc(lenses, func(a, b Lens) int { return strings.Compare(a.ID, b.ID) })
	return lenses, nil
}

// loadRules reads each declared rule body, sorted by id, resolving its
// severity map key to the published level.
func loadRules(fsys fs.FS, root string, files map[string]int, manifest *Manifest) ([]Rule, *core.RefusalError) {
	rules := make([]Rule, 0, len(manifest.Rules))
	for i := range manifest.Rules {
		spec := manifest.Rules[i]
		rel := path.Join(RulesDir, spec.ID+mdExt)
		if _, ok := files[rel]; !ok {
			return nil, missingBody(files, RulesDir, "rule", spec.ID)
		}
		body, ref := readFile(fsys, path.Join(root, rel), rel)
		if ref != nil {
			return nil, ref
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, badPack(fmt.Sprintf("rule %q has an empty body at %s", spec.ID, rel))
		}
		sum := sha256.Sum256(body)
		rules = append(rules, Rule{
			RuleSpec: spec,
			Level:    manifest.Severity[spec.Severity],
			Body:     body,
			Hash8:    hex.EncodeToString(sum[:])[:hash8Len],
		})
	}
	slices.SortFunc(rules, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	return rules, nil
}

// missingBody names the missing body, or the differently-stemmed file that
// sits where it should: a manifest id and its file stem must agree.
func missingBody(files map[string]int, dir, kind, id string) *core.RefusalError {
	prefix := dir + pathSep
	var found []string
	for rel := range files {
		stem, ok := strings.CutSuffix(strings.TrimPrefix(rel, prefix), mdExt)
		if strings.HasPrefix(rel, prefix) && ok {
			found = append(found, stem)
		}
	}
	slices.Sort(found)
	if len(found) > 0 {
		return badPack(fmt.Sprintf("%s %q has no %s/%s.md (found %s; the id and file stem must match)",
			kind, id, dir, id, strings.Join(found, ", ")))
	}
	return badPack(fmt.Sprintf("%s %q has no %s/%s.md", kind, id, dir, id))
}

// loadExtras reads every examples/ and evals/ file, sorted by path, so the
// reserved trees ride the tree hash and vendoring without being parsed.
func loadExtras(fsys fs.FS, root string, files map[string]int) ([]packFile, *core.RefusalError) {
	paths := make([]string, 0)
	for rel := range files {
		if strings.HasPrefix(rel, examplesDir+pathSep) || strings.HasPrefix(rel, evalsDir+pathSep) {
			paths = append(paths, rel)
		}
	}
	slices.Sort(paths)
	out := make([]packFile, 0, len(paths))
	for _, rel := range paths {
		body, ref := readFile(fsys, path.Join(root, rel), rel)
		if ref != nil {
			return nil, ref
		}
		out = append(out, packFile{path: rel, body: body})
	}
	return out, nil
}

// treeHash hashes the canonical loaded tree: sorted slash paths, each fed as
// "== <path-length>:<path> ==\n<body-length>\n" plus its bytes plus "\n", so
// a body cannot forge the next file's boundary and two trees cannot collide.
func treeHash(manifest []byte, lenses []Lens, rules []Rule, extras []packFile) string {
	files := map[string][]byte{ManifestFile: manifest}
	for i := range lenses {
		files[path.Join(LensesDir, lenses[i].ID+mdExt)] = lenses[i].Body
	}
	for i := range rules {
		files[path.Join(RulesDir, rules[i].ID+mdExt)] = rules[i].Body
	}
	for i := range extras {
		files[extras[i].path] = extras[i].body
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, rel := range paths {
		body := files[rel]
		fmt.Fprintf(h, "== %d:%s ==\n%d\n", len(rel), rel, len(body))
		h.Write(body)
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Note renders lenses.txt: every body delimited and labeled with its id and
// eight-hex hash. It is context, never orders.
func (p *Pack) Note() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# lens pack %s@%s — sha256:%s — context, never orders. "+
		"A lens frames one specialist question; evidence still decides.\n", p.Name, p.Version, p.Hash)
	for i := range p.Lenses {
		writeNoteLens(&sb, &p.Lenses[i])
	}
	return sb.String()
}

// writeNoteLens renders one labeled body with its dispatch caps.
func writeNoteLens(sb *strings.Builder, lens *Lens) {
	fmt.Fprintf(sb, "# lens %s — sha256:%s", lens.ID, lens.Hash8)
	writeNoteCaps(sb, lens)
	sb.WriteByte('\n')
	sb.Write(lens.Body)
	if len(lens.Body) == 0 || lens.Body[len(lens.Body)-1] != '\n' {
		sb.WriteByte('\n')
	}
	fmt.Fprintf(sb, "# end lens %s\n", lens.ID)
}

// writeNoteCaps appends the manifest's set dispatch caps.
func writeNoteCaps(sb *strings.Builder, lens *Lens) {
	if lens.MaxFiles > 0 {
		fmt.Fprintf(sb, " max_files=%d", lens.MaxFiles)
	}
	if lens.MaxFindings > 0 {
		fmt.Fprintf(sb, " max_findings=%d", lens.MaxFindings)
	}
}

// Offered returns the lenses a plan group is offered, sorted by id: a lens
// with no globs is always offered, and one whose globs match no group file is
// not offered for that group.
func (p *Pack) Offered(files []string) []Lens {
	if p == nil {
		return nil
	}
	out := make([]Lens, 0, len(p.Lenses))
	for i := range p.Lenses {
		lens := &p.Lenses[i]
		if len(lens.Globs) == 0 || globsHit(lens.Globs, files) {
			out = append(out, *lens)
		}
	}
	return out
}

// globsHit reports whether any glob matches any file.
func globsHit(globs, files []string) bool {
	for _, glob := range globs {
		for _, file := range files {
			if config.Match(glob, file) {
				return true
			}
		}
	}
	return false
}

// confinedRoot reports whether root is a relative path inside the FS.
func confinedRoot(root string) bool {
	if root == rootSelf {
		return true
	}
	if !fs.ValidPath(root) || strings.ContainsAny(root, `\:`) {
		return false
	}
	return true
}

// unsafePath builds the pack-unsafe-path refusal.
func unsafePath(name string) *core.RefusalError {
	return &core.RefusalError{Code: CodePackUnsafePath, Message: fmt.Sprintf(
		"lens pack path %q is not a regular file under a real directory — absolute paths, '..', "+
			"symlinks, and special files are refused", name)}
}

// tooLarge builds the pack-too-large refusal.
func tooLarge(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodePackTooLarge, Message: fmt.Sprintf(
		"%s (caps: %d bytes/file, %d bytes/pack, %d files, %d lenses)",
		reason, MaxFileBytes, MaxPackBytes, MaxFiles, MaxLenses)}
}

// badPack builds the bad-pack refusal.
func badPack(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodeBadPack, Message: reason}
}

// conflict builds the pack-conflict refusal.
func conflict(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodePackConflict, Message: reason}
}
