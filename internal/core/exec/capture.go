package exec

import (
	"fmt"
	"sync"
)

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

type captureMode int

const (
	captureHead captureMode = iota
	captureTail
)

type boundedBuffer struct {
	buf       []byte
	mu        sync.Mutex
	limit     int
	mode      captureMode
	seen      int
	truncated bool
}

func newBoundedBuffer(limit int, mode captureMode) *boundedBuffer {
	return &boundedBuffer{limit: limit, mode: mode}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen += len(p)
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

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + truncationNotice(b.mode, b.limit)
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// Seen reports how many bytes the stream delivered, kept even when the cap
// dropped them, so an over-cap refusal can name the overrun.
func (b *boundedBuffer) Seen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen
}

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

func outputCap(cmdMax, dflt int) int {
	if cmdMax > 0 {
		return cmdMax
	}
	return dflt
}
