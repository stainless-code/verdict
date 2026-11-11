// Package pack loads a lens pack: a strict JSON manifest plus one markdown
// body per lens, hashed as one canonical tree. The built-in pack ships in
// skills/verdict/packs/builtin and fetch resolves it into every bundle.
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
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// Refusal codes a malformed, oversized, or escaping pack refuses with.
const (
	CodeBadPack        = "bad-pack"
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
	SchemaVersion = "1"

	MaxFileBytes = 64 * 1024
	MaxPackBytes = 1 << 20
	MaxFiles     = 256
	MaxLenses    = 64
)

const (
	rulesDir    = "rules"
	examplesDir = "examples"
	evalsDir    = "evals"
	rootSelf    = "."

	namePattern    = `^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`
	versionPattern = `^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`
	lensIDPattern  = `^[a-z0-9][a-z0-9-]*$`

	hash8Len        = 8
	maxLensFiles    = 24
	maxLensFindings = 20
)

var (
	nameRE    = regexp.MustCompile(namePattern)
	versionRE = regexp.MustCompile(versionPattern)
	lensIDRE  = regexp.MustCompile(lensIDPattern)
)

// Manifest is the published pack.json shape.
type Manifest struct {
	SchemaVersion string     `const:"1"      json:"schema_version"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Lenses        []LensSpec `json:"lenses"  minItems:"1"`
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

// Lens is one loaded lens: its manifest row plus the body and its eight-hex
// body digest.
//
//nolint:govet // the manifest row stays first so the published shape reads top-down
type Lens struct {
	LensSpec
	Body  []byte
	Hash8 string
}

// Pack is one loaded lens pack: its manifest, lenses sorted by id, and the
// sha256 of the canonical loaded tree.
//
//nolint:govet // the embedded manifest stays first so the published shape reads top-down
type Pack struct {
	Manifest
	Lenses []Lens
	Hash   string
}

// Load reads the pack rooted at root. A missing pack returns (nil, nil): the
// caller degrades without one. Every other failure is typed.
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
	raw, err := fs.ReadFile(fsys, path.Join(root, ManifestFile))
	if err != nil {
		return nil, badPack(fmt.Sprintf("read %s: %v", ManifestFile, err))
	}
	manifest, ref := parseManifest(raw, path.Join(root, ManifestFile))
	if ref != nil {
		return nil, ref
	}
	lenses, ref := loadLenses(fsys, root, files, manifest)
	if ref != nil {
		return nil, ref
	}
	return &Pack{Manifest: *manifest, Lenses: lenses, Hash: treeHash(raw, lenses)}, nil
}

// LoadAt reads a pack under base, refusing a root that could escape it and
// confining every read through [os.Root]. A missing base returns (nil, nil).
func LoadAt(base, root string) (*Pack, *core.RefusalError) {
	if !confinedRoot(root) {
		return nil, unsafePath(root)
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
	return LoadAt(dir, rootSelf)
}

// walk lists the pack tree, checking the closed layout and every byte cap
// before any file is parsed.
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
	if entry.Type()&fs.ModeSymlink != 0 {
		return unsafePath(name)
	}
	rel := relative(root, name)
	if entry.IsDir() {
		return checkLayoutDir(name, rel)
	}
	return c.addFile(name, rel, entry)
}

// checkLayoutDir refuses a top-level directory outside the fixed layout.
func checkLayoutDir(name, rel string) error {
	if rel == rootSelf || layoutDir(rel) {
		return nil
	}
	return badPack(name + " is outside the closed pack layout")
}

// addFile checks one file's layout and size, then records it.
func (c *treeCounter) addFile(name, rel string, entry fs.DirEntry) error {
	if !layoutFile(rel) {
		return badPack(name + " is outside the closed pack layout")
	}
	info, err := entry.Info()
	if err != nil {
		return badPack("inspect " + name + ": " + err.Error())
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
	return strings.TrimPrefix(name, root+"/")
}

// layoutDir reports whether a top-level directory is part of the fixed
// layout. rules/, examples/, and evals/ are reserved and load nothing yet.
func layoutDir(rel string) bool {
	switch rel {
	case LensesDir, rulesDir, examplesDir, evalsDir:
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
	for _, dir := range []string{LensesDir, rulesDir, examplesDir, evalsDir} {
		if strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// parseManifest decodes pack.json strictly and validates the semantic rules
// the published schema cannot express.
func parseManifest(raw []byte, name string) (*Manifest, *core.RefusalError) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, badPack(fmt.Sprintf("%s is not a pack manifest: %v", name, err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, badPack(name + " carries trailing data after the manifest object")
	}
	if ref := validate(&manifest, name); ref != nil {
		return nil, ref
	}
	return &manifest, nil
}

// validate enforces the manifest rules pack.schema.json documents.
func validate(manifest *Manifest, name string) *core.RefusalError {
	if ref := validateHeader(manifest, name); ref != nil {
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
		if ref := validateLens(&manifest.Lenses[i], name, seen); ref != nil {
			return ref
		}
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

// validateLens checks one row's id, uniqueness, caps, and globs.
func validateLens(lens *LensSpec, name string, seen map[string]bool) *core.RefusalError {
	if !lensIDRE.MatchString(lens.ID) {
		return badPack(fmt.Sprintf("%s lens id %q is not a lowercase slug", name, lens.ID))
	}
	if seen[lens.ID] {
		return badPack(fmt.Sprintf("%s repeats lens id %q", name, lens.ID))
	}
	seen[lens.ID] = true
	if ref := validateLensCaps(lens, name); ref != nil {
		return ref
	}
	return validateLensGlobs(lens, name)
}

// validateLensCaps checks the optional dispatch caps.
func validateLensCaps(lens *LensSpec, name string) *core.RefusalError {
	if lens.MaxFiles < 0 || lens.MaxFiles > maxLensFiles {
		return badPack(fmt.Sprintf("%s lens %q max_files must be 1..%d", name, lens.ID, maxLensFiles))
	}
	if lens.MaxFindings < 0 || lens.MaxFindings > maxLensFindings {
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

// loadLenses reads each manifest row's body, sorted by id. A lenses/ file no
// row names is counted but never loaded.
func loadLenses(fsys fs.FS, root string, files map[string]int, manifest *Manifest) ([]Lens, *core.RefusalError) {
	lenses := make([]Lens, 0, len(manifest.Lenses))
	for i := range manifest.Lenses {
		spec := manifest.Lenses[i]
		rel := path.Join(LensesDir, spec.ID+".md")
		if _, ok := files[rel]; !ok {
			return nil, missingLens(files, spec.ID)
		}
		body, err := fs.ReadFile(fsys, path.Join(root, rel))
		if err != nil {
			return nil, badPack(fmt.Sprintf("read %s: %v", rel, err))
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

// missingLens names the missing body, or the differently-stemmed file that
// sits where it should: a manifest id and its file stem must agree.
func missingLens(files map[string]int, id string) *core.RefusalError {
	prefix := LensesDir + "/"
	var found []string
	for rel := range files {
		stem, ok := strings.CutSuffix(strings.TrimPrefix(rel, prefix), ".md")
		if strings.HasPrefix(rel, prefix) && ok {
			found = append(found, stem)
		}
	}
	slices.Sort(found)
	if len(found) > 0 {
		return badPack(fmt.Sprintf("lens %q has no %s/%s.md (found %s; the id and file stem must match)",
			id, LensesDir, id, strings.Join(found, ", ")))
	}
	return badPack(fmt.Sprintf("lens %q has no %s/%s.md", id, LensesDir, id))
}

// treeHash hashes the canonical loaded tree: sorted slash paths, each fed as
// "== <path> ==\n" plus its bytes plus "\n" (the provenance.PromptsHash
// idiom), so the digest is recomputable by hand.
func treeHash(manifest []byte, lenses []Lens) string {
	files := map[string][]byte{ManifestFile: manifest}
	for _, lens := range lenses {
		files[path.Join(LensesDir, lens.ID+".md")] = lens.Body
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, rel := range paths {
		fmt.Fprintf(h, "== %s ==\n", rel)
		h.Write(files[rel])
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
	for _, lens := range p.Lenses {
		if len(lens.Globs) == 0 || globsHit(lens.Globs, files) {
			out = append(out, lens)
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
		"lens pack path %q escapes the pack root — absolute paths, '..', and symlinks are refused", name)}
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
