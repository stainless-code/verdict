package exec

import (
	"fmt"
	"sync"
)

// Per-command capture caps. Child output is agent-controlled (`verifyCmd`
// comes from patch.json) or upstream-controlled (a huge diff), so every
// commander bounds both streams instead of appending into an unbounded
// buffer (GO-EXEC-05).
const (
	// VerifyOutputCap bounds the combined watchdog capture at 1 MiB. Verify
	// records a redacted tail, so the capture keeps the last bytes.
	VerifyOutputCap = 1 << 20
	// ReadOutputCap bounds each read-verb stream at 4 MiB. Reads parse stdout
	// as a complete document, so the capture keeps the head; stderr keeps its
	// tail so the real error survives.
	ReadOutputCap = 4 << 20
	// FetchOutputCap bounds each fetch stream at 64 MiB. A 20 MiB diff must
	// still chunk successfully; an over-cap stdout is a typed error rather
	// than a silently truncated bundle.
	FetchOutputCap = 64 << 20
)

// captureMode selects which end of an over-cap stream survives.
type captureMode int

const (
	// captureHead keeps the first cap bytes (documents that parse from the
	// start).
	captureHead captureMode = iota
	// captureTail keeps the last cap bytes (proof tails and error text).
	captureTail
)

// boundedBuffer is a goroutine-safe capture that never grows past its cap. The
// surplus is dropped and recorded; String appends a truncation notice so the
// captured bytes never masquerade as the whole stream.
type boundedBuffer struct {
	buf       []byte
	mu        sync.Mutex
	limit     int
	mode      captureMode
	truncated bool
}

// newBoundedBuffer builds one capture with the given cap and bias.
func newBoundedBuffer(limit int, mode captureMode) *boundedBuffer {
	return &boundedBuffer{limit: limit, mode: mode}
}

// Write appends p to the bounded capture. It always reports the full length
// so the child never sees a short pipe write; overflow past the cap is
// discarded and recorded for Truncated/String.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return len(p), nil
	}
	if b.mode == captureTail {
		b.writeTail(p)
	} else {
		b.writeHead(p)
	}
	return len(p), nil
}

// writeHead keeps the first cap bytes of the stream.
func (b *boundedBuffer) writeHead(p []byte) {
	room := b.limit - len(b.buf)
	if room <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return
	}
	if len(p) > room {
		b.buf = append(b.buf, p[:room]...)
		b.truncated = true
		return
	}
	b.buf = append(b.buf, p...)
}

// writeTail keeps the last cap bytes of the stream, shifting the retained
// window in place so the buffer never exceeds the cap.
func (b *boundedBuffer) writeTail(p []byte) {
	if len(p) >= b.limit {
		if len(p) > b.limit || len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return
	}
	if overflow := len(b.buf) + len(p) - b.limit; overflow > 0 {
		b.buf = append(b.buf[:0], b.buf[overflow:]...)
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
}

// String returns the retained bytes plus a truncation notice when the
// stream was capped.
func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + truncationNotice(b.mode, b.limit)
}

// Truncated reports whether the stream hit its cap.
func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// Limit returns the capture cap in bytes.
func (b *boundedBuffer) Limit() int { return b.limit }

// truncationNotice renders the marker that replaces the dropped bytes. The
// word `truncated` is part of the contract: verify's recorded proof and the
// stream sidecars both carry this line.
func truncationNotice(mode captureMode, limit int) string {
	kept := "last"
	if mode == captureHead {
		kept = "first"
	}
	return fmt.Sprintf("\n[verdict: output truncated at %d bytes; kept the %s %d bytes]\n", limit, kept, limit)
}

// outputCap resolves a command's per-stream cap, falling back to the
// commander's documented default.
func outputCap(cmdMax, dflt int) int {
	if cmdMax > 0 {
		return cmdMax
	}
	return dflt
}
