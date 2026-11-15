// Package reverts is the `.verdict/reverts.jsonl` ledger contract: the stored
// row shape, the validating reader, and the cap every surface that reads revert
// memory shares. `backtest reverts` writes the ledger; `fetch` reads it as
// bundle context. Neither decides what a revert is here.
package reverts

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/untrusted"
)

// File is the tracked ledger, relative to the repository root.
const File = ".verdict/reverts.jsonl"

// SchemaVersion is the row schema this build records and accepts.
const SchemaVersion = "1"

// MaxSubject bounds one stored subject: the ledger is tracked, so a
// mass-message commit would otherwise become repository weight no reader asked
// for. A scanner caps at this value, and a stored row past it is a defect.
const MaxSubject = untrusted.MaxBytes

const (
	minSha             = 7
	maxSha             = 40
	fieldSchemaVersion = "schema_version"
	fieldSHA           = "sha"
	fieldSubject       = "subject"
	fieldReverts       = "reverts"
	fieldDate          = "date"
	fieldPaths         = "paths"
)

// ErrCorrupt reports a stored row this build cannot record. The error's own
// message names the field and the defect.
var ErrCorrupt = errors.New("stored row is not recordable")

// Row is one stored revert. Subject and paths are author-controlled text; a
// reader quotes them inside [untrusted.Block], never outside it.
type Row struct {
	SchemaVersion string   `json:"schema_version"`
	SHA           string   `json:"sha"`
	Subject       string   `json:"subject"`
	Reverts       []string `json:"reverts"`
	Date          string   `json:"date"`
	Paths         []string `json:"paths"`
}

// Entry is one stored row with the exact bytes it occupies in the ledger, so a
// writer that keeps a row untouched keeps its bytes too.
//
//nolint:govet // an entry reads as its row first, then the bytes it came from
type Entry struct {
	Row  Row
	Text string
}

// rowError is one stored row this build cannot record: the message names the
// field and the shape it broke, never the stored value, and [errors.Is] matches
// [ErrCorrupt]. The reason is not fenced: it is warned to a terminal and copied
// into a review bundle's `warnings[]`, so a stored value here would carry an
// author's markdown, backtick run, or footer marker outside the fence the whole
// ledger contract exists to keep it in.
type rowError struct {
	detail string
}

// Error renders the field and its defect.
func (d *rowError) Error() string { return d.detail }

// Is matches the package's sentinel, so a caller distinguishes a rejected row
// from a file it could not read.
func (d *rowError) Is(target error) bool { return target == ErrCorrupt }

// Read decodes and validates every row of the ledger at root. An absent
// ledger surfaces [fs.ErrNotExist] so a reader can tell "no memory" from
// "unreadable memory"; a row this build cannot record surfaces [ErrCorrupt].
func Read(root string) ([]Entry, error) {
	raw, err := jq.CappedFile(filepath.Join(root, filepath.FromSlash(File)), jq.MaxKBBytes)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers own the file-naming message; %w keeps fs.ErrNotExist visible
	}
	entries := []Entry{}
	for text := range strings.SplitSeq(string(raw), "\n") {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		row, defect := Decode(text)
		if defect != nil {
			return nil, defect
		}
		entries = append(entries, Entry{Row: row, Text: text})
	}
	return entries, nil
}

// Decode reads one stored row, reporting the defect when this build cannot
// record it. The ledger is tracked and hand-editable, so every field is read
// at its stored type: a wrong type is a defect, never an empty value.
func Decode(text string) (Row, error) {
	doc, err := jq.DecodeOrdered([]byte(text))
	if err != nil {
		return Row{}, &rowError{detail: err.Error()}
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return Row{}, &rowError{detail: "row is not a JSON object"}
	}
	facts, defect := readFacts(obj)
	if defect != nil {
		return Row{}, defect
	}
	row := Row{SchemaVersion: facts.schemaVersion, SHA: facts.sha, Subject: facts.subject, Date: facts.date}
	if row.Reverts, defect = stringsField(obj, fieldReverts); defect != nil {
		return Row{}, defect
	}
	if row.Paths, defect = stringsField(obj, fieldPaths); defect != nil {
		return Row{}, defect
	}
	if defect = validate(&row); defect != nil {
		return Row{}, defect
	}
	return row, nil
}

// Encode renders one row in the published key order.
func Encode(row *Row) string {
	return jq.NewObj(
		fieldSchemaVersion, row.SchemaVersion,
		fieldSHA, row.SHA,
		fieldSubject, row.Subject,
		fieldReverts, itemList(row.Reverts),
		fieldDate, row.Date,
		fieldPaths, itemList(row.Paths),
	).MarshalCompact()
}

// validate reports why one stored row cannot be recorded. Every field a reader
// sees is either a fixed shape that cannot carry text or is quoted inside the
// reading surface's fence, so a value outside its shape is a defect — and the
// reason names the field and the broken shape rather than reprinting the value
// outside that fence.
func validate(row *Row) error {
	if defect := validateFacts(row); defect != nil {
		return defect
	}
	return validateText(row)
}

// validateFacts checks the fields the reading surface prints outside its fence.
func validateFacts(row *Row) error {
	if row.SchemaVersion != SchemaVersion {
		return fieldDefect(fieldSchemaVersion, "is not "+strconv.Quote(SchemaVersion))
	}
	if !commit(row.SHA, maxSha) {
		return fieldDefect(fieldSHA, "is not a 7-to-40-character lowercase hex commit id")
	}
	if _, err := time.Parse(time.RFC3339, row.Date); err != nil {
		return fieldDefect(fieldDate, "is not RFC 3339")
	}
	return nil
}

// validateText checks the fields the reading surface quotes inside its fence.
func validateText(row *Row) error {
	if defect := validateSubject(row); defect != nil {
		return defect
	}
	return validateLists(row)
}

// validateSubject bounds the one free-text field a row carries.
func validateSubject(row *Row) error {
	if row.Subject == "" {
		return fieldDefect(fieldSubject, "is empty")
	}
	if len(row.Subject) > MaxSubject {
		return fieldDefect(fieldSubject, strconv.Itoa(len(row.Subject))+" bytes, past the "+
			strconv.Itoa(MaxSubject)+"-byte cap")
	}
	return nil
}

// validateLists checks the two lists: shaped so they cannot carry text a reader
// could act on, so the reading surface's fence only has to quote the subject.
func validateLists(row *Row) error {
	for _, sha := range row.Reverts {
		if !commit(sha, maxSha) {
			return fieldDefect(fieldReverts, "carries an item that is not a 7-to-40-character lowercase "+
				"hex commit id")
		}
	}
	for _, path := range row.Paths {
		if !repoPath(path) {
			return fieldDefect(fieldPaths, "carries an item that is not a repository-relative path")
		}
	}
	return nil
}

// fieldDefect names one stored field and what is wrong with it. The problem is
// a shape, never the stored value: a reader warns this reason outside every
// fence.
func fieldDefect(key, problem string) error {
	return &rowError{detail: "field " + strconv.Quote(key) + " " + problem}
}

// commit reports whether sha is a lowercase hex commit id of maxLen bytes at
// most and [minSha] at least: the shape git itself abbreviates, and one that
// cannot carry anything a reader could read as an instruction.
func commit(sha string, maxLen int) bool {
	if len(sha) < minSha || len(sha) > maxLen || strings.Trim(sha, "0123456789abcdef") != "" {
		return false
	}
	return true
}

// repoPath reports whether one stored path is a repository-relative path this
// build can render: not absolute, not climbing out through "..", valid UTF-8,
// and free of any control or line-separator rune that could break a line.
func repoPath(rel string) bool {
	return rel != "" && filepath.IsLocal(filepath.FromSlash(rel)) && utf8.ValidString(rel) &&
		!strings.ContainsFunc(rel, core.IsLineBreak)
}

// rowFacts is the four single-valued fields a row publishes.
type rowFacts struct {
	schemaVersion string
	sha           string
	subject       string
	date          string
}

// readFacts reads the single-valued fields, naming the first defect it meets.
func readFacts(obj *jq.Obj) (rowFacts, error) {
	var facts rowFacts
	for _, field := range []struct {
		into *string
		key  string
	}{
		{&facts.schemaVersion, fieldSchemaVersion},
		{&facts.sha, fieldSHA},
		{&facts.subject, fieldSubject},
		{&facts.date, fieldDate},
	} {
		value, defect := stringField(obj, field.key)
		if defect != nil {
			return rowFacts{}, defect
		}
		*field.into = value
	}
	return facts, nil
}

// stringField reads one required string field, naming the defect when the
// stored value is missing or another type.
func stringField(obj *jq.Obj, key string) (string, error) {
	value, ok := obj.Get(key)
	if !ok {
		return "", fieldDefect(key, "is missing")
	}
	text, ok := value.(string)
	if !ok {
		return "", fieldDefect(key, "is not a string")
	}
	return text, nil
}

// stringsField reads one required array-of-strings field, naming the defect
// when the stored value is missing, another type, or carries a non-string item.
func stringsField(obj *jq.Obj, key string) ([]string, error) {
	value, ok := obj.Get(key)
	if !ok {
		return nil, fieldDefect(key, "is missing")
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fieldDefect(key, "is not an array of strings")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, fieldDefect(key, "is not an array of strings")
		}
		out = append(out, text)
	}
	return out, nil
}

// itemList renders a string slice as a JSON array, never null.
func itemList(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}
