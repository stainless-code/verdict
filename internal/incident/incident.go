// Package incident owns the tracked incident ledger: the merges a later
// revert or incident was traced back to, so `trend --incidents` can measure
// the reviews that missed them. internal/core/learn attaches rows through
// [Upsert]; internal/core/trend reads them through [Read].
package incident

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// File is the tracked ledger at the reviewed repository root.
const File = ".verdict/incidents.jsonl"

// Version is the only ledger schema version this build reads or writes.
const Version = "1"

const (
	keySchemaVersion = "schema_version"
	keyBundle        = "bundle"
	keySha           = "sha"
	keyPaths         = "paths"
	keyNote          = "note"
	keyURL           = "url"
	keyDetectedAt    = "detected_at"
)

// Store-read sentinels: callers keep their own postures, so the reader says
// why the ledger did not load instead of choosing a message.
var (
	// ErrMissing marks an absent ledger (anything but a regular file).
	ErrMissing = errors.New("incident ledger missing")
	// ErrCorrupt marks a present ledger that is oversized, does not decode,
	// or holds a row this build cannot read.
	ErrCorrupt = errors.New("incident ledger unreadable")
	// errInvalid marks a row or field this build cannot store or measure; the
	// message names the field and the defect.
	errInvalid = errors.New("incident row invalid")
)

// invalidf names the field and the defect of one unusable row.
func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...)) //nolint:wrapcheck // the file's one error constructor
}

const (
	// minShaLen is the shortest abbreviation git itself resolves.
	minShaLen = 7
	// maxShaLen is a full SHA-1 object id.
	maxShaLen = 40
)

// Row is one attached incident: the merge (`sha`) a review bundle shipped and
// the paths the later revert or incident touched.
type Row struct { //nolint:govet // field order mirrors the published ledger row
	SchemaVersion string   `json:"schema_version"`
	Bundle        string   `json:"bundle"`
	Sha           string   `json:"sha"`
	Paths         []string `json:"paths"`
	Note          *string  `json:"note"`
	URL           *string  `json:"url"`
	DetectedAt    string   `json:"detected_at"`
}

// Read decodes the ledger: one row per line, in file order, or ErrMissing /
// ErrCorrupt.
func Read(root string) ([]Row, error) {
	lines, err := readLines(root)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, line.row)
	}
	return rows, nil
}

// Upsert appends row unless its (bundle, sha) pair is already stored, keeping
// one row per pair and the file sorted by bundle then sha. An existing pair
// leaves every byte of the file untouched. Stored lines are rewritten
// verbatim, so a field this build does not read survives the write. The whole
// read-modify-write holds the ledger's lock, so parallel attaches keep every
// row.
func Upsert(root string, row *Row) (bool, error) {
	if err := Validate(row); err != nil {
		return false, err
	}
	unlock, err := lock(root, lockWait)
	if err != nil {
		return false, err
	}
	defer unlock()
	lines, err := readLines(root)
	if err != nil && !errors.Is(err, ErrMissing) {
		return false, err
	}
	if stored(lines, row) {
		return false, nil
	}
	if err := writeLines(root, append(lines, line{row: *row, text: encode(row)})); err != nil {
		return false, err
	}
	return true, nil
}

// stored reports whether the (bundle, sha) pair is already in the ledger.
func stored(lines []line, row *Row) bool {
	for _, existing := range lines {
		if existing.row.Bundle == row.Bundle && existing.row.Sha == row.Sha {
			return true
		}
	}
	return false
}

// writeLines sorts rows by bundle then sha and replaces the ledger atomically.
func writeLines(root string, lines []line) error {
	slices.SortStableFunc(lines, compareLines)
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.text)
		sb.WriteByte('\n')
	}
	return atomic.WriteFile(path(root), []byte(sb.String()))
}

// Validate reports why row cannot be stored or measured. A row that fails it
// is never written, and a stored row that fails it refuses the read.
func Validate(row *Row) error {
	if err := validateIdentity(row); err != nil {
		return err
	}
	return validatePaths(row)
}

// validateIdentity checks the version, bundle, sha, and detection time.
func validateIdentity(row *Row) error {
	if row.SchemaVersion != Version {
		return invalidf("%s %q is not %q", keySchemaVersion, row.SchemaVersion, Version)
	}
	if !singleElement(row.Bundle) {
		return invalidf("%s %q is not one bundle name", keyBundle, row.Bundle)
	}
	if !ValidSha(row.Sha) {
		return invalidf("%s %q is not 7-40 lowercase hex digits", keySha, row.Sha)
	}
	if _, err := time.Parse(time.RFC3339, row.DetectedAt); err != nil {
		return invalidf("%s %q is not RFC 3339", keyDetectedAt, row.DetectedAt)
	}
	return nil
}

// validatePaths checks that at least one clean path was recorded.
func validatePaths(row *Row) error {
	if len(row.Paths) == 0 {
		return invalidf("%s holds no path", keyPaths)
	}
	for _, p := range row.Paths {
		if p == "" || strings.ContainsFunc(p, unicode.IsControl) {
			return invalidf("%s holds an empty or control-bearing path", keyPaths)
		}
	}
	return nil
}

// singleElement reports whether name is exactly one path element.
func singleElement(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.ContainsFunc(name, unicode.IsControl)
}

// ValidSha reports whether sha is a git-abbreviated or full lowercase object
// id: the one sha rule the attach and the read share.
func ValidSha(sha string) bool {
	if len(sha) < minShaLen || len(sha) > maxShaLen {
		return false
	}
	for _, r := range sha {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// line is one stored row with the bytes it keeps in the file.
type line struct {
	row  Row
	text string
}

// readLines decodes every non-empty line of the ledger, keeping each one's
// own bytes for the rewrite.
func readLines(root string) ([]line, error) {
	file := path(root)
	if !isFile(file) {
		return nil, ErrMissing //nolint:wrapcheck // the package's own sentinel
	}
	raw, err := jq.CappedFile(file, jq.MaxKBBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err) //nolint:wrapcheck // the read already caps and decodes
	}
	var lines []line
	for text := range strings.SplitSeq(string(raw), "\n") {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		row, err := decode(text)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCorrupt, err) //nolint:wrapcheck // the decode error is the cause
		}
		lines = append(lines, line{row: row, text: text})
	}
	return lines, nil
}

// decode reads one stored line into a validated row.
func decode(text string) (Row, error) {
	doc, err := jq.DecodeOrdered([]byte(text))
	if err != nil {
		return Row{}, err //nolint:wrapcheck // readLines wraps every decode failure into ErrCorrupt
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return Row{}, invalidf("row is not a JSON object")
	}
	row, err := rowOf(obj)
	if err != nil {
		return Row{}, err
	}
	if err := Validate(&row); err != nil {
		return Row{}, err
	}
	return row, nil
}

// rowOf reads the typed fields of one stored row.
func rowOf(obj *jq.Obj) (Row, error) {
	var row Row
	var err error
	if row.SchemaVersion, err = stringField(obj, keySchemaVersion); err != nil {
		return Row{}, err
	}
	if row.Bundle, err = stringField(obj, keyBundle); err != nil {
		return Row{}, err
	}
	if row.Sha, err = stringField(obj, keySha); err != nil {
		return Row{}, err
	}
	if row.DetectedAt, err = stringField(obj, keyDetectedAt); err != nil {
		return Row{}, err
	}
	if row.Paths, err = pathsField(obj); err != nil {
		return Row{}, err
	}
	if row.Note, err = optionalString(obj, keyNote); err != nil {
		return Row{}, err
	}
	if row.URL, err = optionalString(obj, keyURL); err != nil {
		return Row{}, err
	}
	return row, nil
}

// stringField reads one string field, null reading empty.
func stringField(obj *jq.Obj, key string) (string, error) {
	v, _ := obj.Get(key)
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", invalidf("%s is not a string", key)
	}
	return s, nil
}

// optionalString reads one null-or-string field.
func optionalString(obj *jq.Obj, key string) (*string, error) {
	v, _ := obj.Get(key)
	if v == nil {
		return nil, nil //nolint:nilnil // an absent optional field is null, not an error
	}
	s, ok := v.(string)
	if !ok {
		return nil, invalidf("%s is neither null nor a string", key)
	}
	return &s, nil
}

// pathsField reads the path array, which is never null in a stored row.
func pathsField(obj *jq.Obj) ([]string, error) {
	v, _ := obj.Get(keyPaths)
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, invalidf("%s is not an array", keyPaths)
	}
	paths := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, invalidf("%s holds a non-string element", keyPaths)
		}
		paths = append(paths, s)
	}
	return paths, nil
}

// Doc renders one row as the object an attach reports: the row's own fields,
// without the schema version, which belongs to the file rather than the row.
func Doc(row *Row) *jq.Obj {
	return jq.NewObj(rowFields(row)...)
}

// encode renders one row in the published key order, with no HTML escaping so
// a note keeps the bytes the operator typed.
func encode(row *Row) string {
	return jq.NewObj(append([]any{keySchemaVersion, row.SchemaVersion}, rowFields(row)...)...).MarshalCompact()
}

// rowFields lists the row's own fields in published key order.
func rowFields(row *Row) []any {
	return []any{
		keyBundle, row.Bundle,
		keySha, row.Sha,
		keyPaths, pathList(row.Paths),
		keyNote, optionalValue(row.Note),
		keyURL, optionalValue(row.URL),
		keyDetectedAt, row.DetectedAt,
	}
}

// pathList renders the paths as a JSON array, never null.
func pathList(paths []string) []any {
	out := make([]any, 0, len(paths))
	for _, p := range paths {
		out = append(out, p)
	}
	return out
}

// optionalValue renders a nil pointer as JSON null.
func optionalValue(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// CompareRows orders rows by bundle, then sha: the ledger's own order, which
// the rewrite and the measurement join both sort by.
//
//nolint:gocritic // slices.SortStableFunc's comparator takes value rows
func CompareRows(a, b Row) int {
	if a.Bundle != b.Bundle {
		return strings.Compare(a.Bundle, b.Bundle)
	}
	return strings.Compare(a.Sha, b.Sha)
}

// compareLines orders the stored lines for the rewrite.
func compareLines(a, b line) int { //nolint:gocritic // lines are copied while sorting one ledger
	return CompareRows(a.row, b.row)
}

// path anchors the ledger under the reviewed repository root.
func path(root string) string {
	if root == "" {
		return File
	}
	return filepath.Join(root, filepath.FromSlash(File))
}

// isFile reports whether p is a regular file, mirroring [[ -f ]].
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
