//go:build windows

package readcommon

import (
	"io/fs"
	"os"
	"syscall"
)

// HasHardLinks reports whether the file at path has more than one hard link.
// Windows FileInfo carries no link count, so the file is opened — as itself,
// never through a reparse point — and its NumberOfLinks read from
// GetFileInformationByHandle. A file that cannot be inspected reports true:
// the caller refuses rather than trusting a count it never read.
func HasHardLinks(path string, _ fs.FileInfo) bool {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	share := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE)
	flags := uint32(syscall.FILE_FLAG_OPEN_REPARSE_POINT | syscall.FILE_FLAG_BACKUP_SEMANTICS)
	handle, err := syscall.CreateFile(name, 0, share, nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		return true
	}
	var data syscall.ByHandleFileInformation
	infoErr := syscall.GetFileInformationByHandle(handle, &data)
	closeErr := syscall.CloseHandle(handle)
	if infoErr != nil || closeErr != nil {
		return true
	}
	return data.NumberOfLinks > 1
}

// OpenedHasHardLinks reports whether the opened file f has more than one hard
// link, read from its own handle (GetFileInformationByHandle), so a name
// swapped after the open cannot answer for it. A handle that cannot be
// inspected reports true.
func OpenedHasHardLinks(f *os.File, _ fs.FileInfo) bool {
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &data); err != nil {
		return true
	}
	return data.NumberOfLinks > 1
}
