package schemagen

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// overlayJSON carries the prose descriptions and any future patterns that do
// not belong in a Go struct tag. Keys are schema names; values map a JSON
// pointer inside that schema to the annotation keywords to merge.
//
//go:embed overlay.json
var overlayJSON []byte

// schemaOverlay carries one schema's prose and pattern overlays.
type schemaOverlay struct {
	Descriptions map[string]string `json:"descriptions,omitempty"`
	Patterns     map[string]string `json:"patterns,omitempty"`
}

// overlay is the parsed overlay document: schema name to its annotations.
type overlay map[string]schemaOverlay

// loadOverlay parses the embedded overlay.
func loadOverlay() (overlay, error) {
	var o overlay
	if err := json.Unmarshal(overlayJSON, &o); err != nil {
		return nil, wrapError("parse overlay.json", err)
	}
	return o, nil
}

// applyOverlay merges one schema's overlay entries into root. Every pointer
// must resolve: a stale overlay key fails generation instead of silently
// dropping prose.
func applyOverlay(name string, root *node, o overlay) error {
	entry, ok := o[name]
	if !ok {
		return errorf("overlay.json has no entry for %s", name)
	}
	if err := mergeDescriptions(name, root, entry.Descriptions); err != nil {
		return err
	}
	return mergePatterns(name, root, entry.Patterns)
}

// mergeDescriptions applies one schema's description overlay.
func mergeDescriptions(name string, root *node, descriptions map[string]string) error {
	for _, pointer := range sortedKeys(descriptions) {
		target, err := overlayTarget(name, root, pointer)
		if err != nil {
			return err
		}
		if err := addDescription(name, pointer, target, descriptions[pointer]); err != nil {
			return err
		}
	}
	return nil
}

// addDescription inserts one description at its schema-defined position.
func addDescription(name, pointer string, target *node, description string) error {
	for _, m := range target.members {
		if m.key == keyDescription {
			return errorf("overlay %s %q: description already set", name, pointer)
		}
	}
	idx := descriptionIndex(target)
	target.members = slices.Insert(target.members, idx, member{keyDescription, stringNode(description)})
	return nil
}

// mergePatterns applies one schema's pattern overlay.
func mergePatterns(name string, root *node, patterns map[string]string) error {
	for _, pointer := range sortedKeys(patterns) {
		target, err := overlayTarget(name, root, pointer)
		if err != nil {
			return err
		}
		target.members = append(target.members, member{keyPattern, stringNode(patterns[pointer])})
	}
	return nil
}

// overlayTarget resolves a pointer and requires an object schema target.
func overlayTarget(name string, root *node, pointer string) (*node, error) {
	target, err := lookupPointer(root, pointer)
	if err != nil {
		return nil, wrapError("overlay "+name+" "+pointer, err)
	}
	if target.kind != kindObject {
		return nil, errorf("overlay %s %q: target is not an object schema", name, pointer)
	}
	return target, nil
}

// sortedKeys returns a map's keys in lexical order for deterministic errors.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// descriptionIndex picks where a description belongs in a schema object:
// after the document header keywords when present, first otherwise.
func descriptionIndex(n *node) int {
	idx := 0
	for i, m := range n.members {
		switch m.key {
		case keySchema, keyID, keyTitle:
			idx = i + 1
		}
	}
	return idx
}

// lookupPointer resolves a JSON pointer (RFC 6901) inside a node tree.
func lookupPointer(root *node, pointer string) (*node, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errorf("pointer must start with /")
	}
	cur := root
	for raw := range strings.SplitSeq(pointer[1:], "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		var err error
		cur, err = childNode(cur, segment)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// childNode resolves one pointer segment against a node.
func childNode(n *node, segment string) (*node, error) {
	switch n.kind {
	case kindObject:
		return objectChild(n, segment)
	case kindArray:
		return arrayChild(n, segment)
	case kindString, kindNumber, kindBool, kindNull:
		return nil, errorf("cannot descend into %v", n.kind)
	}
	return nil, errorf("cannot descend into %v", n.kind)
}

// objectChild resolves a pointer segment against an object node.
func objectChild(n *node, segment string) (*node, error) {
	for _, m := range n.members {
		if m.key == segment {
			return m.value, nil
		}
	}
	return nil, errorf("no member %q", segment)
}

// arrayChild resolves a pointer segment as an array index.
func arrayChild(n *node, segment string) (*node, error) {
	idx, err := strconv.Atoi(segment)
	if err != nil {
		return nil, errorf("array segment %q is not an index", segment)
	}
	if idx < 0 || idx >= len(n.elems) {
		return nil, errorf("array index %d out of range", idx)
	}
	return n.elems[idx], nil
}
