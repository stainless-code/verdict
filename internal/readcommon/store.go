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
// [fs.ErrNotExist], which a ReadDir-only check would read as an empty home; the
// non-directory branch is its own sentence, so no verb talks about a file store
// as if the directory were merely unreadable.
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
		return nil, NotADirectory(state)
	}
	entries, err := readDir(state)
	if err != nil {
		return nil, UnreadableStore(state)
	}
	return entries, nil
}

// NotADirectory names a bundle home the store path holds as a file, in the one
// sentence every verb refuses it with. It is a separate sentence from
// [UnreadableStore] because the fix differs: a file sitting where the store
// belongs is moved aside and the bundle re-fetched, while an unreadable
// directory is restored from backup. Permission advice would be wrong here —
// the path opens fine, it is simply not a directory.
func NotADirectory(state string) error {
	//nolint:wrapcheck // the sentence is the whole error; there is no cause to wrap
	return fmt.Errorf("bundle store %s is not a directory (move the file aside and re-fetch to rebuild the store)", state)
}

// UnreadableStore names a bundle home that will not list, in the one sentence
// every verb refuses it with.
func UnreadableStore(state string) error {
	//nolint:wrapcheck // the sentence is the whole error; there is no cause to wrap
	return fmt.Errorf("bundle store %s is unreadable (restore the directory or fix its permissions)", state)
}
