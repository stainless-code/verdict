package readcommon

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// ReadDirLimit reads at most limit directory entries, sorted by name. Entries
// past limit are never read, so a hostile directory cannot turn one walk into
// an unbounded scan; more reports that the batch filled the limit and the
// directory may hold further entries. Callers treat any error as an absent
// directory, so the cause is sealed at this boundary.
func ReadDirLimit(dir string, limit int) ([]os.DirEntry, bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, jq.Sealed("open directory", err)
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, jq.Sealed("read directory", err)
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, len(entries) >= limit, nil
}
