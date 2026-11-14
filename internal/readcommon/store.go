package readcommon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// StoreEntries lists a bundle home: an absent home is empty, and anything else
// that stops the listing comes back as one error naming it. Stat runs before
// ReadDir because Windows reports a regular file opened as a directory as
// [fs.ErrNotExist], which a ReadDir-only check would read as an empty home.
func StoreEntries(state string) ([]os.DirEntry, error) {
	return storeEntries(state, os.Stat, os.ReadDir)
}

// storeEntries is StoreEntries over injectable filesystem calls, so the
// Windows shape — a regular file whose ReadDir answers instead of failing —
// is a test, not an argument.
func storeEntries(
	state string,
	stat func(string) (os.FileInfo, error),
	readDir func(string) ([]os.DirEntry, error),
) ([]os.DirEntry, error) {
	info, err := stat(state)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, UnreadableStore(state)
	case !info.IsDir():
		return nil, UnreadableStore(state)
	}
	entries, err := readDir(state)
	if err != nil {
		return nil, UnreadableStore(state)
	}
	return entries, nil
}

// UnreadableStore names a bundle home that will not list, in the one sentence
// every verb refuses it with.
func UnreadableStore(state string) error {
	//nolint:wrapcheck // the sentence is the whole error; there is no cause to wrap
	return fmt.Errorf("bundle store %s is unreadable (restore the directory or fix its permissions)", state)
}
