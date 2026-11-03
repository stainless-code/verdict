package schemacheck

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// document is one decoded schema file, kept so a sibling-file $ref resolves
// against the file that carries it.
type document struct {
	root any
	file string
}

// resolve follows one $ref: "name.json#/pointer" reads the sibling file
// (relative to the referring file), and "#/pointer" stays in the same file. A
// reference this checker cannot resolve is an error, never an accept.
func resolve(from document, ref string, files map[string]any) (document, any, error) {
	file, fragment, _ := strings.Cut(ref, refHash)
	next := from
	if file != "" {
		target := file
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(from.file), filepath.FromSlash(file))
		}
		root, err := loadFile(target, files)
		if err != nil {
			return document{}, nil, jq.Sealed(fmt.Sprintf("unresolvable $ref %q", ref), err)
		}
		next = document{file: target, root: root}
	}
	node, err := pointer(next.root, fragment)
	if err != nil {
		return document{}, nil, jq.Sealed(fmt.Sprintf("unresolvable $ref %q", ref), err)
	}
	return next, node, nil
}

// loadFile decodes one schema file, caching it for the rest of the pass.
func loadFile(file string, files map[string]any) (any, error) {
	if root, ok := files[file]; ok {
		return root, nil
	}
	root, err := Document(file)
	if err != nil {
		return nil, jq.Sealed("load "+file, err)
	}
	files[file] = root
	return root, nil
}

// pointer walks a "#/a/b" JSON pointer, decoding the ~0 and ~1 escapes.
func pointer(root any, fragment string) (any, error) {
	trimmed := strings.TrimPrefix(fragment, refSeparator)
	if trimmed == "" {
		return root, nil
	}
	node := root
	for raw := range strings.SplitSeq(trimmed, refSeparator) {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, escapedSlash, refSeparator), escapedTilde, tilde)
		next, ok := descend(node, token)
		if !ok {
			return nil, fmt.Errorf("no such pointer %q", fragment) //nolint:wrapcheck // locally constructed message, not an external error
		}
		node = next
	}
	return node, nil
}

// descend reads one pointer token from an object or an array.
func descend(node any, token string) (any, bool) {
	switch holder := node.(type) {
	case map[string]any:
		value, ok := holder[token]
		return value, ok
	case []any:
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 || index >= len(holder) {
			return nil, false
		}
		return holder[index], true
	default:
		return nil, false
	}
}

// normalizeFragment accepts "" (whole document), "#", "#/..." and "/..." and
// returns the "" or "/..." form the pointer walk expects. Anything else is a
// usage error (a bad sub-pointer refuses).
func normalizeFragment(fragment string) (string, error) {
	trimmed := strings.TrimPrefix(fragment, refHash)
	if trimmed != "" && !strings.HasPrefix(trimmed, refSeparator) {
		return "", jq.NewErrf("bad sub-pointer (want #/...): %s", fragment)
	}
	return trimmed, nil
}
