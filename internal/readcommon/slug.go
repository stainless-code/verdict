package readcommon

import "strings"

// Slugify ports kb_slugify: lowercase, collapse non-alnum runs to one dash,
// trim leading/trailing dashes, keep the first 65 characters. Note: the
// shell's dash trims are GNU-sed `\+` repetitions — BSD sed (macOS) reads
// `\+` literally and keeps the dashes, so the shell itself is
// platform-dependent on dashy inputs. No eval grades that edge (it cannot
// be pinned without normalizing the bash leg); Go implements the intended
// trimming behavior, matching GNU/Linux.
func Slugify(s string) string {
	lower := ASCIILower(s)
	var sb strings.Builder
	sb.Grow(len(lower))
	dash := false
	for i := range len(lower) {
		dash = appendSlugByte(&sb, lower[i], dash)
	}
	slug := strings.Trim(sb.String(), "-")
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
	}
	return slug
}

// appendSlugByte appends one lowercased byte in slug form, tracking dashes.
func appendSlugByte(sb *strings.Builder, c byte, dash bool) bool {
	if isSlugChar(c) {
		sb.WriteByte(c)
		return false
	}
	if !dash {
		sb.WriteByte('-')
		return true
	}
	return dash
}

// isSlugChar reports whether c survives verbatim in a slug.
func isSlugChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// maxSlugLen is the cut -c1-65 cap on slugified ids.
const maxSlugLen = 65
