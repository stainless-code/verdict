package archtest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gate"
	"github.com/stainless-code/verdict/internal/run"
)

// defaultsBegin and defaultsEnd fence the generated table in the manual, and
// defaultsHeading is the section that owns it: the table may not migrate into
// an unrelated section of the operator contract.
const (
	defaultsBegin   = "<!-- defaults-at-a-glance:begin (generated; see internal/archtest/verbdefaults_test.go) -->"
	defaultsEnd     = "<!-- defaults-at-a-glance:end -->"
	defaultsHeading = "## Defaults at a glance"
)

// updateDefaults regenerates the table in docs/manual.md instead of failing.
var updateDefaults = flag.Bool("update", false, "regenerate the published defaults table")

// TestPublishedDefaultsTable keeps the manual's "Defaults at a glance" table
// generated: every row is read from the registry the resolver merges or from
// the default the flag parser ships, so a default that moves fails here
// instead of publishing a stale table.
func TestPublishedDefaultsTable(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "docs", "manual.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docs/manual.md: %v", err)
	}
	doc := string(raw)
	start, stop := defaultsBlock(t, doc)
	want := "\n\n" + defaultsTable() + "\n\n"
	if doc[start:stop] == want {
		return
	}
	if *updateDefaults {
		rewritten := doc[:start] + want + doc[stop:]
		if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
			t.Fatalf("update docs/manual.md: %v", err)
		}
		t.Logf("updated %s", path)
		return
	}
	t.Errorf("docs/manual.md's defaults table is stale (regenerate with "+
		"go test ./internal/archtest -run TestPublishedDefaultsTable -update)\ngot:%s\nwant:%s",
		doc[start:stop], want)
}

// defaultsBlock returns the span between the generated markers, which must sit
// under the section that owns the table rather than in an unrelated one.
func defaultsBlock(t *testing.T, doc string) (int, int) {
	t.Helper()
	begin := strings.Index(doc, defaultsBegin)
	if begin < 0 {
		t.Fatalf("docs/manual.md lost the %q marker", defaultsBegin)
	}
	if heading := strings.Index(doc, defaultsHeading); heading < 0 || heading > begin {
		t.Fatalf("docs/manual.md must carry %q above the generated table", defaultsHeading)
	}
	start := begin + len(defaultsBegin)
	stop := strings.Index(doc[start:], defaultsEnd)
	if stop < 0 {
		t.Fatalf("docs/manual.md lost the %q marker", defaultsEnd)
	}
	return start, start + stop
}

// defaultsRows lists one row per published default: the config registry's
// built-in floor, then the flag defaults the parsers start from.
func defaultsRows() [][]string {
	var rows [][]string
	for _, builtin := range config.BuiltinDefaults() {
		rows = append(rows, []string{
			strings.Join(builtin.Verbs, ", "), "defaults." + builtin.Key, defaultsCell(builtin.Value),
		})
	}
	rows = append(rows,
		[]string{"post", "--bind", defaultsCell(core.PostFlags{}.Bind)},
		[]string{"gate", "--live", gate.DefaultLive},
		[]string{"gate", "--coverage", gate.DefaultCoverage},
		[]string{"run", "--timeout", run.DefaultTimeoutS},
		[]string{"run", "--rounds", run.DefaultRounds},
	)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i][0] != rows[j][0] {
			return rows[i][0] < rows[j][0]
		}
		return rows[i][1] < rows[j][1]
	})
	return rows
}

// defaultsTable renders the rows the way the manual shows them: a markdown
// table with every cell padded to its column, which is what `bun fmt` leaves.
func defaultsTable() string {
	header := []string{"Verb", "Knob", "Built-in default"}
	rows := defaultsRows()
	widths := make([]int, len(header))
	for _, row := range append([][]string{header}, rows...) {
		for i, text := range row {
			widths[i] = max(widths[i], len(text))
		}
	}
	rule := make([]string, len(header))
	for i := range header {
		rule[i] = strings.Repeat("-", widths[i])
	}
	lines := []string{defaultsLine(header, widths), defaultsLine(rule, widths)}
	for _, row := range rows {
		lines = append(lines, defaultsLine(row, widths))
	}
	return strings.Join(lines, "\n")
}

// defaultsLine pads one row to the column widths.
func defaultsLine(cells []string, widths []int) string {
	var line strings.Builder
	line.WriteString("|")
	for i, text := range cells {
		line.WriteString(" " + text + strings.Repeat(" ", widths[i]-len(text)) + " |")
	}
	return line.String()
}

// defaultsCell renders one default the way the table reads: "unset" for a floor
// value the resolver never sets, on/off for a switch.
func defaultsCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return "unset"
	case bool:
		if typed {
			return "on"
		}
		return "off"
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []string:
		if len(typed) == 0 {
			return "unset"
		}
		return strings.Join(typed, ", ")
	default:
		return fmt.Sprint(typed)
	}
}
