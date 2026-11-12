// Package fence owns the one escaping rule every untrusted fence in verdict
// shares: an attacker-influenced byte string is rendered so it cannot spell a
// fence marker. The repair loop's prior review, the agent feedback block, and
// fetch's impact rows all pass their content through [Break], so one rule
// decides what a forged closer looks like wherever content crosses the trust
// boundary.
package fence

import "strings"

// Marker is the length of the shortest run of "<" that can spell a fence
// marker.
const Marker = 3

// Break is the zero-width space slipped into a run of "<": it renders
// invisibly and breaks the fence pattern without dropping a byte.
const Break = "\u200b"

// Safe neutralizes fence marker text so a forged opener or closer stays
// payload: in every run of three or more "<" a zero-width space follows each
// second byte, so three consecutive "<" never survive and the fence pattern
// cannot reappear at any run length or offset. Shorter runs are ordinary text
// and stay byte-identical. Content with no fence text is returned as is: the
// embedded bytes can be as large as a 32 MiB artifact read cap.
func Safe(s string) string {
	if !strings.Contains(s, "<<<") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '<' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := angleRunEnd(s, i)
		writeAngleRun(&sb, end-i)
		i = end
	}
	return sb.String()
}

// writeAngleRun writes one run of n "<" bytes: a run long enough to spell a
// fence marker is broken, a shorter one is ordinary text and stays as it was.
func writeAngleRun(sb *strings.Builder, n int) {
	if n < Marker {
		for range n {
			sb.WriteByte('<')
		}
		return
	}
	writeBrokenRun(sb, n)
}

// writeBrokenRun writes n "<" bytes with a zero-width space after every second
// one, leaving runs of at most two.
func writeBrokenRun(sb *strings.Builder, n int) {
	for k := range n {
		sb.WriteByte('<')
		if k%2 == 1 {
			sb.WriteString(Break)
		}
	}
}

// angleRunEnd returns the end of the run of "<" bytes that starts at i.
func angleRunEnd(s string, i int) int {
	end := i
	for end < len(s) && s[end] == '<' {
		end++
	}
	return end
}
