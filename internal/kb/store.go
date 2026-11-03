package kb

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// Store-read sentinels: callers keep their own postures, so the reader
// reports why a store did not load instead of choosing a message.
var (
	// ErrMissing marks an absent store (anything but a regular file,
	// matching the scripts' [[ -f ]] gate).
	ErrMissing = errors.New("kb store missing")
	// ErrCorrupt marks a present store that is oversized or does not decode.
	ErrCorrupt = errors.New("kb store unreadable")
)

// ParseRows decodes kb.jsonl bytes order-preserving.
func ParseRows(raw []byte) ([]*jq.Obj, error) {
	rows, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		//nolint:wrapcheck // the sentinel leads so callers can classify, the cause stays visible
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return rows, nil
}

// ReadRows reads the worktree store: order-preserving rows, or ErrMissing /
// ErrCorrupt. Every verb that reads kb.jsonl goes through here, so the
// decode and the size cap exist once.
func ReadRows() ([]*jq.Obj, error) {
	if !isFile(File) {
		//nolint:wrapcheck // the package sentinel is the classification callers match
		return nil, ErrMissing
	}
	raw, err := jq.CappedFile(File, jq.MaxKBBytes)
	if err != nil {
		//nolint:wrapcheck // the sentinel leads so callers can classify, the cause stays visible
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return ParseRows(raw)
}

// ReadRowsOK reads the store leniently: ok is false for both an absent and
// an unreadable store (the warn-and-drop callers).
func ReadRowsOK() (rows []*jq.Obj, ok bool) {
	rows, err := ReadRows()
	if err != nil {
		return nil, false
	}
	return rows, true
}

// FindRow returns the first row whose .id is id. Only a string id matches: a
// non-string id never equals the id the caller asked for.
func FindRow(rows []*jq.Obj, id string) *jq.Obj {
	for _, row := range rows {
		if v, ok := row.Get("id"); ok && v == id {
			return row
		}
	}
	return nil
}

// AppendHistory appends one event object to the row's history array
// (missing or non-array history reads empty).
func AppendHistory(row *jq.Obj, ev *jq.Obj) {
	h, _ := row.Get(fieldHistory)
	hist, ok := h.([]any)
	if !ok {
		hist = []any{}
	}
	row.Set(fieldHistory, append(hist, ev))
}

// WriteRows rewrites the store atomically, one compact row per line. Key
// order is the stored contract (the decoder is order-preserving), so the
// writer never re-encodes through a map.
func WriteRows(rows []*jq.Obj) error {
	var sb strings.Builder
	for _, row := range rows {
		sb.WriteString(row.MarshalCompact())
		sb.WriteByte('\n')
	}
	return atomic.WriteFile(File, []byte(sb.String()))
}

// AppendRow appends one already-encoded row (the --rule path appends instead
// of rewriting).
func AppendRow(line string) error {
	var prev []byte
	if isFile(File) {
		raw, err := jq.CappedFile(File, jq.MaxKBBytes)
		if err != nil {
			//nolint:wrapcheck // locally constructed read failure naming the store
			return fmt.Errorf("read %s: %w", File, err)
		}
		prev = raw
	}
	return atomic.WriteFile(File, append(prev, line...))
}

// ValidID reports whether id matches the rule-id shape
// ^[a-z0-9][a-z0-9-]{0,64}$ shared by the citation gate, --rule, and the
// triage/retire lookups.
func ValidID(id string) bool { return idRe.MatchString(id) }

// isFile reports whether path is a regular file, mirroring [[ -f ]].
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
