package reviewdoc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/untrusted"
)

type linkKey struct {
	path      string
	startLine int
	endLine   int
}

type linkTable struct {
	ids      map[linkKey]int
	baseURL  string
	headFull string
	defs     []string
}

func newLinkTable(baseURL, headFull string) *linkTable {
	return &linkTable{
		baseURL:  strings.TrimRight(baseURL, "/"),
		headFull: headFull,
		ids:      map[linkKey]int{},
	}
}

func (t *linkTable) linkable() bool {
	return t.baseURL != "" && t.headFull != ""
}

func (t *linkTable) anchor(path string, startLine, endLine int, allowLink bool) string {
	label := formatAnchorLabel(path, startLine, endLine)
	if !allowLink || !t.linkable() || path == "" {
		return untrusted.CodeSpan(label)
	}
	id := t.register(path, startLine, endLine)
	return "[" + untrusted.LinkLabel(label) + "][ev-" + strconv.Itoa(id) + "]"
}

func (t *linkTable) register(path string, startLine, endLine int) int {
	key := linkKey{path: path, startLine: startLine, endLine: endLine}
	if id, ok := t.ids[key]; ok {
		return id
	}
	id := len(t.defs) + 1
	t.ids[key] = id
	url := t.baseURL + "/blob/" + t.headFull + "/" + untrusted.EscapeURLPath(path) + lineFragment(startLine, endLine)
	t.defs = append(t.defs, "[ev-"+strconv.Itoa(id)+"]: "+url)
	return id
}

func (t *linkTable) definitions() []string {
	return t.defs
}

func formatAnchorLabel(path string, startLine, endLine int) string {
	if endLine <= 0 {
		return path
	}
	if startLine > 0 && startLine != endLine {
		return fmt.Sprintf("%s:%d-%d", path, startLine, endLine)
	}
	return fmt.Sprintf("%s:%d", path, endLine)
}

func lineFragment(startLine, endLine int) string {
	if endLine <= 0 {
		return ""
	}
	if startLine > 0 && startLine != endLine {
		return fmt.Sprintf("#L%d-L%d", startLine, endLine)
	}
	return fmt.Sprintf("#L%d", endLine)
}
