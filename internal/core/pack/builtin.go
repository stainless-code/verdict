package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// LanguagePacksRoot is the embedded directory holding the curated
// language/ecosystem packs: one pack root per child directory, every one
// loaded at the builtin layer beside BuiltinRoot.
const LanguagePacksRoot = "packs/languages"

// builtinSource is the builtin layer's source label for the core pack; a
// curated pack appends its directory slug, so a row names the tree it came
// from.
const builtinSource = "builtin"

// BuiltinOrigins loads the whole builtin layer: the core pack at BuiltinRoot
// plus every curated pack under LanguagePacksRoot, in directory order. A
// missing root is an absent layer, never a refusal; a file where a pack
// directory belongs refuses — the embedded tree is shipped, not operator
// input, so a stray entry there is a build defect.
func BuiltinOrigins(fsys fs.FS) ([]Origin, *core.RefusalError) {
	if fsys == nil {
		return nil, nil
	}
	out := make([]Origin, 0, 1+MaxLenses)
	p, ref := Load(fsys, BuiltinRoot)
	if ref != nil {
		return nil, ref
	}
	if p != nil {
		out = append(out, Origin{Layer: LayerBuiltin, Source: builtinSource, Pack: p})
	}
	langs, ref := languageOrigins(fsys)
	if ref != nil {
		return nil, ref
	}
	return append(out, langs...), nil
}

// languageOrigins reads one curated pack per child directory of
// LanguagePacksRoot. [fs.ReadDir] sorts by file name, so the layer order is
// the directory order on every platform.
func languageOrigins(fsys fs.FS) ([]Origin, *core.RefusalError) {
	entries, err := fs.ReadDir(fsys, LanguagePacksRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, badPack(fmt.Sprintf("read %s: %v", LanguagePacksRoot, err))
	}
	out := make([]Origin, 0, len(entries))
	for _, entry := range entries {
		origin, ref := languageOrigin(fsys, entry)
		if ref != nil {
			return nil, ref
		}
		out = append(out, origin)
	}
	return out, nil
}

// languageOrigin loads the one curated pack a child directory holds, labeled
// with that directory's slug.
func languageOrigin(fsys fs.FS, entry fs.DirEntry) (Origin, *core.RefusalError) {
	name := entry.Name()
	if !entry.IsDir() {
		return Origin{}, badPack(fmt.Sprintf("%s/%s is not a pack directory", LanguagePacksRoot, name))
	}
	p, ref := Load(fsys, path.Join(LanguagePacksRoot, name))
	if ref != nil {
		return Origin{}, ref
	}
	if p == nil {
		return Origin{}, badPack(fmt.Sprintf("%s/%s carries no %s", LanguagePacksRoot, name, ManifestFile))
	}
	return Origin{Layer: LayerBuiltin, Source: builtinSource + ":" + name, Pack: p}, nil
}

// builtinSlug reports whether name is a curated builtin pack's slug, matching
// the directory its source label names.
func builtinSlug(origin Origin) string {
	return strings.TrimPrefix(origin.Source, builtinSource+":")
}
