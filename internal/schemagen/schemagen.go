// Package schemagen generates the published JSON Schemas from the Go shape
// types in model.go. The checked-in schemas/ files are the contract; this
// package is the only supported way to edit them.
package schemagen

//go:generate go run ../../cmd/schemagen -write

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File is one generated schema artifact.
type File struct {
	Name string
	Data []byte
}

// genError is the package's sealed error type. Returning it keeps generator
// failures local instead of leaking fmt/errors values across the boundary.
type genError struct { //nolint:govet // cause-first reads better than padding for size
	msg   string
	cause error
}

// Error implements error.
func (e *genError) Error() string { return e.msg }

// Unwrap exposes the cause to [errors.Is]/[errors.As].
func (e *genError) Unwrap() error { return e.cause }

// errorf builds a prefixed generator error.
func errorf(format string, args ...any) error {
	if len(args) == 0 {
		return &genError{msg: format}
	}
	return &genError{msg: fmt.Sprintf(format, args...)}
}

// wrapError annotates a cause with a generator prefix and keeps the chain.
func wrapError(prefix string, cause error) error {
	return &genError{msg: prefix + ": " + cause.Error(), cause: cause}
}

// Generate builds every published schema in deterministic filename order.
func Generate() ([]File, error) {
	overlay, err := loadOverlay()
	if err != nil {
		return nil, err
	}
	docs := documents()
	files := make([]File, 0, len(docs))
	for i := range docs {
		doc := &docs[i]
		b := newBuilder(doc.defs)
		root, err := b.documentNode(doc)
		if err != nil {
			return nil, err
		}
		if err := applyOverlay(doc.name, root, overlay); err != nil {
			return nil, err
		}
		files = append(files, File{Name: doc.name + ".schema.json", Data: printDocument(root)})
	}
	return files, nil
}

// RepoRoot walks up from the working directory to the module root.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", wrapError("locate working directory", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errorf("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// DiffFiles renders a unified diff between the committed (want) and
// generated (got) bytes.
func DiffFiles(name string, want, got []byte) string {
	a := splitLines(want)
	b := splitLines(got)
	ops := diffOps(a, b)
	var out strings.Builder
	fmt.Fprintf(&out, "--- schemas/%s (committed)\n", name)
	fmt.Fprintf(&out, "+++ schemas/%s (generated)\n", name)
	for _, hunk := range diffHunks(ops) {
		writeHunk(&out, ops, hunk)
	}
	return out.String()
}

// diffOp is one edit-script line: space (equal), minus (committed only) or
// plus (generated only).
type diffOp struct {
	line string
	kind byte
}

// splitLines splits text into lines without trailing newlines.
func splitLines(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// diffOps computes a shortest edit script with a suffix-LCS table.
func diffOps(a, b []string) []diffOp {
	return editScript(a, b, lcsTable(a, b))
}

// lcsTable builds the suffix longest-common-subsequence table.
func lcsTable(a, b []string) [][]int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			dp[i][j] = lcsCell(a, b, dp, i, j)
		}
	}
	return dp
}

// lcsCell fills one suffix-LCS table cell.
func lcsCell(a, b []string, dp [][]int, i, j int) int {
	if a[i] == b[j] {
		return dp[i+1][j+1] + 1
	}
	return max(dp[i+1][j], dp[i][j+1])
}

// editScript walks the LCS table into ordered diff operations.
func editScript(a, b []string, dp [][]int) []diffOp {
	n, m := len(a), len(b)
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', line: a[i]})
			i++
			j++
		case j >= m || (i < n && dp[i+1][j] >= dp[i][j+1]):
			ops = append(ops, diffOp{kind: '-', line: a[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', line: b[j]})
			j++
		}
	}
	return ops
}

// hunk is a half-open range of edit-script operations with context.
type hunk struct {
	start int
	end   int
}

// diffHunks groups changed operations with three lines of context.
func diffHunks(ops []diffOp) []hunk {
	const context = 3
	var hunks []hunk
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		end := hunkEnd(ops, i, context)
		start := max(0, i-context)
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			hunks[len(hunks)-1].end = max(hunks[len(hunks)-1].end, end)
		} else {
			hunks = append(hunks, hunk{start, end})
		}
		i = end
	}
	return hunks
}

// hunkEnd finds the first operation after the last change plus context.
func hunkEnd(ops []diffOp, from, context int) int {
	last := from
	for j := from + 1; j < len(ops); j++ {
		if ops[j].kind != ' ' {
			last = j
		}
		if j-last > context {
			break
		}
	}
	return min(len(ops), last+1+context)
}

// writeHunk prints one unified-diff hunk.
func writeHunk(out *strings.Builder, ops []diffOp, h hunk) {
	oldStart, newStart := hunkStart(ops, h.start)
	oldCount, newCount := hunkCounts(ops, h)
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", oldStart+1, oldCount, newStart+1, newCount)
	for i := h.start; i < h.end; i++ {
		out.WriteByte(ops[i].kind)
		out.WriteString(ops[i].line)
		out.WriteByte('\n')
	}
}

// hunkStart counts the old/new lines consumed before a hunk.
func hunkStart(ops []diffOp, start int) (oldLines, newLines int) {
	for i := range start {
		switch ops[i].kind {
		case ' ':
			oldLines++
			newLines++
		case '-':
			oldLines++
		case '+':
			newLines++
		}
	}
	return oldLines, newLines
}

// hunkCounts counts the old/new lines a hunk covers.
func hunkCounts(ops []diffOp, h hunk) (oldLines, newLines int) {
	for i := h.start; i < h.end; i++ {
		switch ops[i].kind {
		case ' ':
			oldLines++
			newLines++
		case '-':
			oldLines++
		case '+':
			newLines++
		}
	}
	return oldLines, newLines
}
