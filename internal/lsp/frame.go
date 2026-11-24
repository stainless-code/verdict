package lsp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	// maxFrameBytes caps one body: the same 16 MiB bound the ACP and MCP
	// transports apply to a line.
	maxFrameBytes = 16 << 20
	// maxHeaderBytes caps one header block. LSP headers are two short lines;
	// a block past this is a desynchronized or hostile stream.
	maxHeaderBytes = 8 << 10
	// maxLengthDigits is the widest Content-Length the cap admits.
	maxLengthDigits = 8
	headerLength    = "content-length"
	headerSep       = ":"
	headerPrefix    = "Content-Length: "
	headerEnd       = "\r\n\r\n"
	decimalBase     = 10
)

// frameKind classifies a framing failure. A framing failure stops the
// session: a byte stream whose body length is unknown cannot be
// resynchronized, and guessing one is unsafe.
type frameKindError string

func (k frameKindError) Error() string { return string(k) }

const (
	errFrameHeader   frameKindError = "malformed frame header"
	errFrameTooLarge frameKindError = "frame exceeds the 16 MiB cap"
)

// frameError is one framing failure with the detail an operator reads on
// stderr; it unwraps to its kind.
type frameError struct {
	kind   frameKindError
	detail string
}

func (e *frameError) Error() string { return e.kind.Error() + ": " + e.detail }

func (e *frameError) Unwrap() error { return e.kind }

func headerErr(detail string) error { return &frameError{kind: errFrameHeader, detail: detail} }

// ioError is one read failure below the framing, kept inspectable.
type ioError struct {
	err error
	op  string
}

func (e *ioError) Error() string { return e.op + ": " + e.err.Error() }

func (e *ioError) Unwrap() error { return e.err }

// readFrame reads one `Content-Length` framed body. It returns [io.EOF] only
// on a clean end between frames; an EOF inside a header block or body is
// [io.ErrUnexpectedEOF]. Header names compare case-insensitively, bare-LF
// line endings are tolerated, and every other header is ignored.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length, err := readHeaders(r)
	if err != nil {
		return nil, err
	}
	// The body grows as bytes arrive: a declared 16 MiB that never comes
	// costs what was sent, not the declaration.
	var body bytes.Buffer
	n, err := io.CopyN(&body, r, int64(length))
	if err != nil {
		if errors.Is(err, io.EOF) && n < int64(length) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, &ioError{op: "read frame body", err: err}
	}
	return body.Bytes(), nil
}

// headerBlock accumulates one header block's state.
type headerBlock struct {
	length int
	seen   int
	lines  int
}

// readHeaders consumes one header block up to its blank line and returns the
// declared body length. Blank lines before the first header are skipped.
func readHeaders(r *bufio.Reader) (int, error) {
	block := headerBlock{length: -1}
	for {
		line, err := readHeaderLine(r, &block.seen)
		if err != nil {
			return 0, eofIn(err, block.lines == 0 && line == "")
		}
		end, err := block.take(line)
		if err != nil || end {
			return block.result(err)
		}
	}
}

// take consumes one header line; end reports the blank line that closes a
// block with at least one header in it.
func (b *headerBlock) take(line string) (bool, error) {
	if line == "" {
		return b.lines > 0, nil
	}
	b.lines++
	return false, b.add(line)
}

// result is the block's declared length, or the failure that ended it.
func (b *headerBlock) result(err error) (int, error) {
	if err != nil {
		return 0, err
	}
	if b.length < 0 {
		return 0, headerErr("missing Content-Length")
	}
	return b.length, nil
}

// eofIn maps an EOF while reading headers: clean between frames, unexpected
// inside one.
func eofIn(err error, between bool) error {
	if !errors.Is(err, io.EOF) {
		return err
	}
	if between {
		return io.EOF
	}
	return io.ErrUnexpectedEOF
}

// add reads one header line; only Content-Length matters.
func (b *headerBlock) add(line string) error {
	name, value, ok := strings.Cut(line, headerSep)
	if !ok {
		return headerErr("header line without a colon")
	}
	if !strings.EqualFold(strings.TrimSpace(name), headerLength) {
		return nil
	}
	if b.length >= 0 {
		return headerErr("duplicate Content-Length")
	}
	n, err := parseLength(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	b.length = n
	return nil
}

// readHeaderLine reads one header line without its line ending, counting the
// bytes against the header cap.
func readHeaderLine(r *bufio.Reader, seen *int) (string, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		*seen += len(chunk)
		if *seen > maxHeaderBytes {
			return "", headerErr("header block exceeds 8 KiB")
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		text := string(bytes.TrimRight(line, "\r\n"))
		if err != nil {
			return text, &ioError{op: "read frame header", err: err}
		}
		return text, nil
	}
}

// parseLength validates one Content-Length value: plain decimal digits,
// bounded by maxFrameBytes.
func parseLength(value string) (int, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, headerErr("non-numeric Content-Length " + strconv.Quote(value))
	}
	if len(value) > maxLengthDigits {
		return 0, &frameError{kind: errFrameTooLarge, detail: "Content-Length " + value}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n > maxFrameBytes {
		return 0, &frameError{kind: errFrameTooLarge, detail: "Content-Length " + value}
	}
	return n, nil
}

// encodeFrame renders one body as a framed message.
func encodeFrame(body []byte) []byte {
	out := make([]byte, 0, len(body)+len(headerPrefix)+len(headerEnd)+maxLengthDigits)
	out = append(out, headerPrefix...)
	out = strconv.AppendInt(out, int64(len(body)), decimalBase)
	out = append(out, headerEnd...)
	return append(out, body...)
}
