// Package schemacheck validates JSON values against the subset of JSON Schema
// draft 2020-12 the published schemas use. It is the one implementation behind
// both the Go conformance tests and the shipped `schemas/validate.sh` shim
// (`verdict __validate-schema`), so a document can no longer pass one side and
// fail the other.
//
// The subset is: $ref (same-file "#/..." pointer or sibling-file
// "name.json#/..." pointer), type (single name or array), const, enum,
// properties, required, items (single-schema form), additionalProperties (bool
// or schema), minimum, maximum, exclusiveMinimum, minItems, minLength,
// maxLength, pattern, anyOf, format (date-time), $defs. Annotation keywords
// ($schema, $id, title, description, default, examples, $comment) are ignored.
// Any other keyword fails loudly, so a schema that outgrows the checker
// breaks the build instead of silently passing, and a $ref this checker
// cannot resolve is an error rather than an accept.
package schemacheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Violation is one validation failure addressed by the JSON path of the value
// that broke the contract.
type Violation struct {
	Path string
	Msg  string
}

// String renders the violation without the CLI's "FAIL" prefix.
func (v Violation) String() string { return v.Path + ": " + v.Msg }

// rootPath addresses the validated value in failure messages.
const rootPath = "$"

// Checker validates values against one loaded schema document, resolving
// $refs against the directory that document lives in.
//
//nolint:govet // field alignment buys nothing here: every field is a pointer word
type Checker struct {
	files  map[string]any
	root   document
	target any
}

// Load decodes the schema at path (optionally selecting a "#/..." fragment as
// the validation root) and returns a checker for it.
func Load(path, fragment string) (*Checker, error) {
	root, err := Document(path)
	if err != nil {
		return nil, err
	}
	pointerText, err := normalizeFragment(fragment)
	if err != nil {
		return nil, err
	}
	target := root
	if pointerText != "" {
		target, err = pointer(root, pointerText)
		if err != nil {
			return nil, jq.Sealed(fmt.Sprintf("unresolvable sub-pointer %q in %s", fragment, path), err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, jq.Sealed("resolve "+path, err)
	}
	return &Checker{
		files:  map[string]any{abs: root},
		root:   document{file: abs, root: root},
		target: target,
	}, nil
}

// Check validates value against the loaded root and returns every violation;
// an empty result means the document is valid.
func (c *Checker) Check(value any) []Violation {
	w := &walk{files: c.files}
	w.check(c.root, c.target, value, rootPath)
	return w.fails
}

// Validate reports whether value validates against schema, resolving local
// $ref pointers against doc. It is the in-memory entry point for callers that
// already hold decoded documents (the conformance tests); a sibling-file $ref
// needs a Checker loaded from disk.
func Validate(doc, schema, value any) []Violation {
	w := &walk{files: map[string]any{}}
	w.check(document{root: doc}, schema, value, rootPath)
	return w.fails
}

// Document decodes one JSON file with number fidelity, the shape both the CLI
// and the tests hand the checker.
func Document(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read "+path, err)
	}
	value, err := Decode(raw)
	if err != nil {
		return nil, jq.Sealed(path+" is not JSON", err)
	}
	return value, nil
}

// Decode decodes one JSON value, keeping numbers exact so an integer stays an
// integer and a bound keeps its literal text.
func Decode(raw []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, jq.Sealed("decode the JSON value", err)
	}
	if err := dec.Decode(new(any)); err == nil {
		return nil, jq.NewErr("trailing data after the JSON value")
	}
	return value, nil
}

// walk carries one validation pass: the failure list and the schema-file
// cache that sibling $refs fill.
type walk struct {
	files map[string]any
	fails []Violation
}

// check validates one value against one schema node.
func (w *walk) check(d document, schema, value any, path string) {
	if allowed, ok := schema.(bool); ok {
		if !allowed {
			w.fail(path, "false schema rejects every value")
		}
		return
	}
	s, ok := schema.(map[string]any)
	if !ok {
		w.fail(path, "schema node is not an object")
		return
	}
	w.checkKeywords(s, path)
	w.followRef(d, s, value, path)
	w.applyKeywords(d, s, value, path)
}

// followRef validates value against the node a $ref names, keeping siblings of
// the reference in force.
func (w *walk) followRef(d document, s map[string]any, value any, path string) {
	ref, ok := s[keyRef].(string)
	if !ok {
		return
	}
	next, target, err := resolve(d, ref, w.files)
	if err != nil {
		w.fail(path, err.Error())
		return
	}
	w.check(next, target, value, path)
}

// fail records one failure at path.
func (w *walk) fail(path, msg string) {
	w.fails = append(w.fails, Violation{Path: path, Msg: msg})
}
