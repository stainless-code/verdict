// Package fence owns the one escaping rule every untrusted fence in verdict
// shares: an attacker-influenced byte string is rendered so it cannot spell a
// fence marker. The repair loop's prior review, the agent feedback block,
// fetch's impact rows, and the revert note all pass their content through
// [Safe] or [BreakRuns], so one rule decides what a forged marker looks like
// wherever content crosses the trust boundary.
package fence

import "strings"

// Marker is the length of the shortest run of marker bytes that can spell a
// fence.
const Marker = 3

// Break is the zero-width space slipped into a run of marker bytes: it renders
// invisibly and breaks the fence pattern without dropping a byte.
const Break = "\u200b"

// Safe neutralizes fence marker text so a forged opener or closer stays
// payload: in every run of three or more "<" a zero-width space follows each
// second byte, so three consecutive "<" never survive and the fence pattern
// cannot reappear at any run length or offset. Shorter runs are ordinary text
// and stay byte-identical. Content with no fence text is returned as is: the
// embedded bytes can be as large as a 32 MiB artifact read cap.
func Safe(s string) string { return BreakRuns(s, '<') }

// BreakRuns neutralizes every run of marker bytes long enough to spell a fence
// in any renderer that uses that byte: a fence marker, or a markdown code fence
// whose marker is a run of backticks. A shorter run is ordinary text and stays
// byte-identical, and content with nothing to break is returned as is.
func BreakRuns(s string, marker byte) string {
	if !strings.Contains(s, strings.Repeat(string(marker), Marker)) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != marker {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := runEnd(s, i, marker)
		writeRun(&sb, end-i, marker)
		i = end
	}
	return sb.String()
}

// writeRun writes one run of n marker bytes: a run long enough to spell a fence
// is broken, a shorter one is ordinary text and stays as it was.
func writeRun(sb *strings.Builder, n int, marker byte) {
	if n < Marker {
		for range n {
			sb.WriteByte(marker)
		}
		return
	}
	writeBrokenRun(sb, n, marker)
}

// writeBrokenRun writes n marker bytes with a zero-width space after every
// second one, leaving runs of at most two.
func writeBrokenRun(sb *strings.Builder, n int, marker byte) {
	for k := range n {
		sb.WriteByte(marker)
		if k%2 == 1 {
			sb.WriteString(Break)
		}
	}
}

// runEnd returns the end of the run of marker bytes that starts at i.
func runEnd(s string, i int, marker byte) int {
	end := i
	for end < len(s) && s[end] == marker {
		end++
	}
	return end
}
