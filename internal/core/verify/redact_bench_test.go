package verifycore

import (
	"strings"
	"testing"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// benchTailLine is one hostile log line: an ANSI color sequence, a NUL byte, a
// credential shape no environment entry names, and the environment secret the
// scrub also catches by value.
const benchTailLine = "\x1b[31mwarn\x1b[0m: retry https://alice:hunter2@example.invalid/r.git " +
	"with Authorization: Bearer sk-ant-api03-abcdefghijklmnop\x00 build failed\n"

// BenchmarkSanitizeTail measures the scrub every graded command pays for on the
// tail verify records. The fixture is built to exactly the watchdog's capture
// cap ([coreexec.VerifyOutputCap]) and asserts that size: no recorded tail can
// be longer, and a shorter fixture measures a different workload.
func BenchmarkSanitizeTail(b *testing.B) {
	environ := []string{"GITHUB_TOKEN=hunter2"}
	tail := benchTail(b, coreexec.VerifyOutputCap)
	b.SetBytes(int64(len(tail)))
	b.ReportAllocs()
	for b.Loop() {
		if sanitizeTail(tail, environ) == "" {
			b.Fatal("sanitizeTail returned nothing for a non-empty tail")
		}
	}
}

// benchTail repeats the hostile line up to exactly size bytes.
func benchTail(b *testing.B, size int) string {
	b.Helper()
	tail := strings.Repeat(benchTailLine, size/len(benchTailLine)+1)[:size]
	if len(tail) != size {
		b.Fatalf("fixture is %d bytes, want exactly the %d-byte capture cap", len(tail), size)
	}
	return tail
}
