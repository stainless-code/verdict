package readcommon

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stainless-code/verdict/internal/jq"
)

const rootSelf = "."

// ErrSymlinkPath reports a symlink or changed identity at a no-follow boundary.
var ErrSymlinkPath error = unsafePathError{}

type unsafePathError struct{}

func (unsafePathError) Error() string {
	return "path must not contain a symlink or change while opening"
}

// OpenParentWith opens the verified parent of a symlink-free path. The final
// component may be absent. open is the descriptor acquisition seam.
func OpenParentWith(name string, open func(string) (*os.Root, error)) (*os.Root, string, error) {
	absolute, err := checkedPath(name)
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Dir(absolute)
	identity, err := pinnedParentIdentity(dir)
	if err != nil {
		return nil, "", err
	}
	root, err := open(dir)
	if err != nil {
		return nil, "", jq.Sealed("open parent", err)
	}
	if err := verifyRoot(root, identity); err != nil {
		_ = root.Close()
		return nil, "", err
	}
	return root, filepath.Base(absolute), nil
}

func pinnedParentIdentity(dir string) (os.FileInfo, error) {
	volume := filepath.VolumeName(dir)
	anchor, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, jq.Sealed("open path anchor", err)
	}
	defer anchor.Close()
	relative := strings.TrimLeft(filepath.ToSlash(dir[len(volume):]), slash)
	if relative == "" {
		relative = rootSelf
	}
	parent, err := OpenDirectoryIn(anchor, relative)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	info, err := parent.Stat(rootSelf)
	if err != nil {
		return nil, jq.Sealed("stat pinned parent", err)
	}
	freezeFileID(info)
	return info, nil
}

func verifyRoot(root *os.Root, identity os.FileInfo) error {
	opened, err := root.Stat(rootSelf)
	if err != nil {
		return jq.Sealed("stat opened root", err)
	}
	if !os.SameFile(identity, opened) {
		return ErrSymlinkPath
	}
	return nil
}

func checkedPath(name string) (string, error) {
	if !filepath.IsAbs(name) {
		// Bypass PWD: implicit cwd prefixes must use the physical OS directory.
		cwd, err := syscall.Getwd()
		if err != nil {
			return "", jq.Sealed("resolve path", err)
		}
		name = cwd + string(filepath.Separator) + name
	}
	volume := filepath.VolumeName(name)
	current := volume + string(filepath.Separator)
	parts := strings.Split(strings.TrimRight(filepath.ToSlash(name[len(volume):]), slash), "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		if err := checkPathComponent(current, i == len(parts)-1); err != nil {
			return "", err
		}
	}
	return current, nil
}

func checkPathComponent(name string, last bool) error {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) && last {
		return nil
	}
	if err != nil {
		return jq.Sealed("inspect path", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkPath
	}
	return nil
}

// OpenDirectoryIn pins a directory below root, refusing symlink components.
// The caller owns the returned root, including when name is ".".
func OpenDirectoryIn(root *os.Root, name string) (*os.Root, error) {
	if !fs.ValidPath(name) {
		return nil, ErrSymlinkPath
	}
	current, err := root.OpenRoot(rootSelf)
	if err != nil {
		return nil, jq.Sealed("open directory", err)
	}
	for part := range strings.SplitSeq(name, slash) {
		next, err := openDirectoryPart(current, part)
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func openDirectoryPart(root *os.Root, name string) (*os.Root, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, jq.Sealed("inspect directory", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSymlinkPath
	}
	freezeFileID(before)
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, jq.Sealed("open directory", err)
	}
	if err := verifyRoot(child, before); err != nil {
		_ = child.Close()
		return nil, err
	}
	return child, nil
}

// OpenRegularNoSymlinkIn opens a regular file below root and verifies its
// descriptor identity before any bytes are read. Unix opens also use O_NOFOLLOW
// and O_NONBLOCK; other platforms are protected by the identity checks.
func OpenRegularNoSymlinkIn(root *os.Root, name string) (*os.File, error) {
	parent, err := OpenDirectoryIn(root, filepath.ToSlash(filepath.Dir(name)))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openRegularLeaf(parent, filepath.Base(name))
}

func openRegularLeaf(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, jq.Sealed("inspect file", err)
	}
	if !before.Mode().IsRegular() {
		return nil, ErrSymlinkPath
	}
	freezeFileID(before)
	f, err := openNoFollowIn(root, name)
	if err != nil {
		return nil, err
	}
	if err := verifyRegularFile(root, name, f, before); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func verifyRegularFile(root *os.Root, name string, f *os.File, before os.FileInfo) error {
	opened, err := f.Stat()
	if err != nil {
		return jq.Sealed("stat opened file", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return ErrSymlinkPath
	}
	after, err := root.Lstat(name)
	if err != nil {
		return jq.Sealed("inspect opened file", err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		return ErrSymlinkPath
	}
	return nil
}

func freezeFileID(info os.FileInfo) {
	// Windows defers file IDs; load the Lstat identity before the path can change.
	_ = os.SameFile(info, info)
}
