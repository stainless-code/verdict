package skill

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DiffCapBytes caps the combined unified diff one status report renders.
	DiffCapBytes = 64 * 1024
	diffContext  = 3
	maxDiffCells = 1_000_000
)

const truncatedMarker = "\n[diff truncated]\n"

// Diff is one changed file's unified diff against the embedded payload.
type Diff struct {
	Path      string
	Body      string
	Truncated bool
}

// diffOp is one unified-diff line: context, removal, or addition.
type diffOp struct {
	text string
	kind byte
}

func unifiedDiff(name string, oldBody, newBody []byte, limit int) (string, bool) {
	if limit <= 0 {
		return "", true
	}
	ops := lineEdits(splitDiffLines(oldBody), splitDiffLines(newBody))
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
	writeHunks(&b, ops)
	body := b.String()
	if len(body) <= limit {
		return body, false
	}
	cut := limit
	if idx := strings.LastIndexByte(body[:cut], '\n'); idx >= 0 {
		cut = idx + 1
	}
	return body[:cut] + truncatedMarker, true
}

func splitDiffLines(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

//
//nolint:revive // the LCS backtrack is one loop with two fallbacks
func lineEdits(a, b []string) []diffOp {
	if len(a)*len(b) > maxDiffCells {
		return replacement(a, b)
	}
	dp := lcsTable(a, b)
	ops := make([]diffOp, 0, len(a)+len(b))
	i, j := len(a), len(b)
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append(ops, diffOp{kind: ' ', text: a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, diffOp{kind: '+', text: b[j-1]})
			j--
		default:
			ops = append(ops, diffOp{kind: '-', text: a[i-1]})
			i--
		}
	}
	slices.Reverse(ops)
	return ops
}

func replacement(a, b []string) []diffOp {
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, line := range a {
		ops = append(ops, diffOp{kind: '-', text: line})
	}
	for _, line := range b {
		ops = append(ops, diffOp{kind: '+', text: line})
	}
	return ops
}

func lcsTable(a, b []string) [][]int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				dp[i][j] = dp[i-1][j-1] + 1
			case dp[i-1][j] >= dp[i][j-1]:
				dp[i][j] = dp[i-1][j]
			default:
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return dp
}

func writeHunks(b *strings.Builder, ops []diffOp) {
	changes := changeIndexes(ops)
	if len(changes) == 0 {
		return
	}
	oldAt, newAt := linePositions(ops)
	for k := 0; k < len(changes); {
		start := max(0, changes[k]-diffContext)
		end := min(len(ops), changes[k]+diffContext+1)
		for k+1 < len(changes) && changes[k+1]-diffContext <= end {
			k++
			end = min(len(ops), changes[k]+diffContext+1)
		}
		k++
		writeHunk(b, ops[start:end], oldAt[start], newAt[start])
	}
}

func changeIndexes(ops []diffOp) []int {
	var changes []int
	for i, op := range ops {
		if op.kind != ' ' {
			changes = append(changes, i)
		}
	}
	return changes
}

func linePositions(ops []diffOp) (oldAt, newAt []int) {
	oldAt = make([]int, len(ops)+1)
	newAt = make([]int, len(ops)+1)
	oldLine, newLine := 0, 0
	for i, op := range ops {
		oldAt[i], newAt[i] = oldLine, newLine
		switch op.kind {
		case ' ':
			oldLine++
			newLine++
		case '-':
			oldLine++
		case '+':
			newLine++
		}
	}
	oldAt[len(ops)], newAt[len(ops)] = oldLine, newLine
	return oldAt, newAt
}

//
//nolint:revive // the header counts and body are one render
func writeHunk(b *strings.Builder, ops []diffOp, oldStart, newStart int) {
	oldCount, newCount := 0, 0
	for _, op := range ops {
		if op.kind != '+' {
			oldCount++
		}
		if op.kind != '-' {
			newCount++
		}
	}
	if oldCount > 0 {
		oldStart++
	}
	if newCount > 0 {
		newStart++
	}
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, op := range ops {
		b.WriteByte(op.kind)
		b.WriteString(op.text)
		b.WriteByte('\n')
	}
}
