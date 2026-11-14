package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/storelock"
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
func ReadRows(root string) ([]*jq.Obj, error) {
	path := storePath(root)
	if !isFile(path) {
		//nolint:wrapcheck // the package sentinel is the classification callers match
		return nil, ErrMissing
	}
	raw, err := jq.CappedFile(path, jq.MaxKBBytes)
	if err != nil {
		//nolint:wrapcheck // the sentinel leads so callers can classify, the cause stays visible
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return ParseRows(raw)
}

// ReadRowsOK reads the store leniently: ok is false for both an absent and
// an unreadable store (the warn-and-drop callers).
func ReadRowsOK(root string) (rows []*jq.Obj, ok bool) {
	rows, err := ReadRows(root)
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

// Lock takes the store's OS advisory lock and returns its release. A
// read-modify-write holds it above its read, so the append, the veto rewrite,
// and the credit rewrite are one writer each; a lock another writer holds past
// the wait refuses [storelock.ErrBusy].
//
// The lock comes first and the `.verdict/.gitignore` contract second, so
// parallel writers serialize the contract instead of racing each other's atomic
// rewrite of one file, and the contract is in place before the write returns: a
// lock file beside a tracked store is never worktree dirt. The home is created
// ahead of the lock because the lock file lives inside it.
//
// The layout error comes back unwrapped: the layout already names the path and
// owns the operator sentence, and its caller maps it with [config.RefusalOf]
// before prefixing anything of its own.
func Lock(root string) (func(), error) {
	layout := config.NewLayout(root)
	if serr := layout.EnsureStateHome(); serr != nil {
		//nolint:wrapcheck // the layout error carries the refusal its caller maps
		return nil, serr
	}
	unlock, err := storelock.Acquire(storePath(root), storelock.DefaultWait)
	if err != nil {
		//nolint:wrapcheck // the shared lock already names the store it guards
		return nil, err
	}
	if serr := layout.EnsureStateIgnore(); serr != nil {
		unlock()
		//nolint:wrapcheck // the layout error carries the refusal its caller maps
		return nil, serr
	}
	return unlock, nil
}

// WriteRows rewrites the store atomically, one compact row per line. Key
// order is the stored contract (the decoder is order-preserving), so the
// writer never re-encodes through a map. The primitive takes no lock: the
// caller holds [Lock] across its read and this write, exactly as
// internal/incident's Upsert does.
func WriteRows(root string, rows []*jq.Obj) error {
	var sb strings.Builder
	for _, row := range rows {
		sb.WriteString(row.MarshalCompact())
		sb.WriteByte('\n')
	}
	return atomic.WriteFile(storePath(root), []byte(sb.String()))
}

// AppendRow appends one already-encoded row (the --rule path appends instead
// of rewriting). The read-modify-write holds the store's OS advisory lock, so
// parallel appends keep every row; a lock another writer holds past the wait
// refuses [storelock.ErrBusy].
func AppendRow(root, line string) error {
	path := storePath(root)
	unlock, err := Lock(root)
	if err != nil {
		return err
	}
	defer unlock()
	var prev []byte
	if isFile(path) {
		raw, err := jq.CappedFile(path, jq.MaxKBBytes)
		if err != nil {
			//nolint:wrapcheck // locally constructed read failure naming the store
			return fmt.Errorf("read %s: %w", path, err)
		}
		prev = raw
	}
	return atomic.WriteFile(path, append(prev, line...))
}

// ValidID reports whether id matches the rule-id shape
// ^[a-z0-9][a-z0-9-]{0,64}$ shared by the citation gate, --rule, and the
// triage/retire lookups.
func ValidID(id string) bool { return idRe.MatchString(id) }

// storePath anchors the knowledge-base store under the reviewed root.
func storePath(root string) string {
	if root == "" {
		return File
	}
	return filepath.Join(root, File)
}

// isFile reports whether path is a regular file, mirroring [[ -f ]].
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
