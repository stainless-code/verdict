package verifycore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The git index format (Documentation/gitformat-index.txt), read only as far
// as the gitlink entries: a 12-byte header, the entries, then extensions and
// a trailing hash of everything before it.
const (
	gitIndexHeaderLen  = 12
	gitIndexWordLen    = 4
	gitIndexModeOffset = 24 // ctime(8) mtime(8) dev(4) ino(4), then mode
	gitIndexStatLen    = 40 // ctime, mtime, dev, ino, mode, uid, gid, size
	gitIndexFlagsLen   = 2
	gitIndexExtended   = 0x4000
	gitIndexNameMask   = 0x0fff
	gitIndexModeLink   = 0o160000
	gitIndexModeMask   = 0o170000
	gitIndexEntryAlign = 8
	gitIndexMinVersion = 2
	gitIndexExtVersion = 3
	gitIndexMaxVersion = 4
	gitIndexExtHdrLen  = 8
	gitIndexVarintMask = 0x7f
	gitIndexVarintMore = 0x80
	gitIndexVarintBits = 7
	// gitIndexVarintMax bounds a strip count's encoding: five bytes reach
	// far past any path length, so a longer run is malformed.
	gitIndexVarintMax = 5
	// gitIndexMaxMiB caps the index read; a larger one refuses.
	gitIndexMaxMiB    = 64
	gitIndexMaxBytes  = gitIndexMaxMiB << 20
	gitIndexSHA1Len   = 20
	gitIndexSHA256Len = 32
)

var (
	gitIndexSignature = []byte("DIRC")
	gitIndexSplitLink = []byte("link")
)

// sandboxGitlinks returns the slash-separated paths of the index's gitlink
// entries (mode 160000) — the submodule paths git enters on its own, for
// `git status` in the superproject among others. present is false when the
// index does not exist; reason is set when it exists but cannot be read.
//
// The object format (a SHA-1 or SHA-256 entry hash) is told by which layout
// parses: every entry's recorded name length must match the name it ends
// at, and the extensions must end exactly at the trailer, so a wrong layout
// fails rather than misreads. No digest is computed (SHA-1 is unavailable
// under FIPS-only Go), so a zeroed `index.skipHash` trailer reads the same.
// Both layouts parsing with different results, or a split index (the
// `link` extension keeps entries in a shared file), is refused rather than
// half-read.
func sandboxGitlinks(indexPath string) (links []string, present bool, reason string) {
	data, err := readcommon.ReadRegularCapped(filepath.Clean(indexPath), gitIndexMaxBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, ""
	case errors.Is(err, jq.ErrTooLarge):
		return nil, true, fmt.Sprintf("larger than %d MiB", gitIndexMaxMiB)
	case err != nil:
		return nil, true, err.Error()
	}
	links, reason = gitlinksFromIndex(data)
	return links, true, reason
}

// gitlinksFromIndex parses index bytes under both hash layouts and returns
// the gitlinks, or why they cannot be trusted.
func gitlinksFromIndex(data []byte) (links []string, reason string) {
	var found [][]string
	for _, hashLen := range []int{gitIndexSHA1Len, gitIndexSHA256Len} {
		if parsed, ok := parseGitIndex(data, hashLen); ok {
			found = append(found, parsed)
		}
	}
	switch {
	case len(found) == 0:
		return nil, "not an index this reader understands (a split index, or an unknown version)"
	case len(found) > 1 && !slices.Equal(found[0], found[1]):
		return nil, "its object format is ambiguous"
	}
	return found[0], ""
}

// gitIndexReader walks one index body with a fixed hash length.
type gitIndexReader struct {
	prev    string
	data    []byte
	pos     int
	end     int
	hashLen int
	version uint32
}

func parseGitIndex(data []byte, hashLen int) ([]string, bool) {
	if len(data) < gitIndexHeaderLen+hashLen || !bytes.Equal(data[:gitIndexWordLen], gitIndexSignature) {
		return nil, false
	}
	r := &gitIndexReader{
		data: data, pos: gitIndexHeaderLen, end: len(data) - hashLen, hashLen: hashLen,
		version: binary.BigEndian.Uint32(data[gitIndexWordLen : 2*gitIndexWordLen]),
	}
	if r.version < gitIndexMinVersion || r.version > gitIndexMaxVersion {
		return nil, false
	}
	links, ok := r.entries(binary.BigEndian.Uint32(data[2*gitIndexWordLen : gitIndexHeaderLen]))
	return links, ok && r.extensions()
}

// entries reads count entries and returns the gitlink paths among them.
func (r *gitIndexReader) entries(count uint32) ([]string, bool) {
	var links []string
	for range count {
		path, mode, ok := r.entry()
		if !ok {
			return nil, false
		}
		if mode&gitIndexModeMask == gitIndexModeLink {
			links = append(links, path)
		}
	}
	return links, true
}

// entry reads one index entry and returns its path and mode.
func (r *gitIndexReader) entry() (string, uint32, bool) {
	start := r.pos
	fixed := gitIndexStatLen + r.hashLen + gitIndexFlagsLen
	if r.pos+fixed > r.end {
		return "", 0, false
	}
	mode := binary.BigEndian.Uint32(r.data[r.pos+gitIndexModeOffset : r.pos+gitIndexModeOffset+gitIndexWordLen])
	flags := binary.BigEndian.Uint16(r.data[r.pos+fixed-gitIndexFlagsLen : r.pos+fixed])
	r.pos += fixed
	if r.version >= gitIndexExtVersion && flags&gitIndexExtended != 0 {
		if r.pos += gitIndexFlagsLen; r.pos > r.end {
			return "", 0, false
		}
	}
	path, ok := r.path()
	// git records min(len, 0xfff) in the flags and never an empty name; a
	// misread entry would not agree with the name it ends at.
	if !ok || path == "" || int(flags&gitIndexNameMask) != min(len(path), gitIndexNameMask) {
		return "", 0, false
	}
	return path, mode, r.pad(start)
}

// pad skips a v2/v3 entry's NUL padding: git sizes an entry as
// (offset + name length + 8) & ~7, and r.pos sits one past the name's NUL.
// Version 4 entries are not padded.
func (r *gitIndexReader) pad(start int) bool {
	if r.version < gitIndexMaxVersion {
		r.pos = start + (r.pos-1-start+gitIndexEntryAlign)&^(gitIndexEntryAlign-1)
	}
	return r.pos <= r.end
}

// path reads one entry's NUL-terminated name; v4 names drop a prefix-strip
// count of bytes from the previous name and append the stored suffix.
func (r *gitIndexReader) path() (string, bool) {
	prefix := ""
	if r.version == gitIndexMaxVersion {
		strip, ok := r.varint()
		if !ok || strip > uint64(len(r.prev)) {
			return "", false
		}
		prefix = r.prev[:uint64(len(r.prev))-strip]
	}
	nul := bytes.IndexByte(r.data[r.pos:r.end], 0)
	if nul < 0 {
		return "", false
	}
	path := prefix + string(r.data[r.pos:r.pos+nul])
	r.pos += nul + 1
	r.prev = path
	return path, true
}

// varint decodes git's offset varint (each continuation adds one before
// shifting, so encodings are unique), unsigned and at most
// gitIndexVarintMax bytes long, so no input can overflow it.
func (r *gitIndexReader) varint() (uint64, bool) {
	var val uint64
	for n := 0; n < gitIndexVarintMax && r.pos < r.end; n++ {
		b := r.data[r.pos]
		r.pos++
		val += uint64(b & gitIndexVarintMask)
		if b&gitIndexVarintMore == 0 {
			return val, true
		}
		val = (val + 1) << gitIndexVarintBits
	}
	return 0, false
}

// extensions walks the extension blocks after the entries; they must end
// exactly at the trailer, and a split index's `link` extension fails.
func (r *gitIndexReader) extensions() bool {
	for r.pos+gitIndexExtHdrLen <= r.end {
		sig := r.data[r.pos : r.pos+gitIndexWordLen]
		size := int(binary.BigEndian.Uint32(r.data[r.pos+gitIndexWordLen : r.pos+gitIndexExtHdrLen]))
		if bytes.Equal(sig, gitIndexSplitLink) {
			return false
		}
		r.pos += gitIndexExtHdrLen + size
	}
	return r.pos == r.end
}
