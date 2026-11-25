package archtest

import (
	"flag"
	"fmt"
	"github.com/stainless-code/verdict/internal/loop"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/gate"
	"github.com/stainless-code/verdict/internal/run"
)

const (
	defaultsBegin   = "<!-- defaults-at-a-glance:begin (generated; see internal/archtest/verbdefaults_test.go) -->"
	defaultsEnd     = "<!-- defaults-at-a-glance:end -->"
	defaultsHeading = "## Defaults at a glance"
)

var updateDefaults = flag.Bool("update", false, "regenerate the published defaults table")

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

func defaultsRows() [][]string {
	var rows [][]string
	for _, builtin := range config.BuiltinDefaults() {
		rows = append(rows, []string{
			strings.Join(builtin.Verbs, ", "), "defaults." + builtin.Key, defaultsCell(builtin.Value),
		})
	}
	rows = append(rows,
		[]string{"post", "--bind", defaultsCell(postcore.Flags{}.Bind)},
		[]string{"gate", "--live", gate.DefaultLive},
		[]string{"gate", "--coverage", gate.DefaultCoverage},
		[]string{"demo", "--timeout", runcore.DefaultTimeoutS},
		[]string{"run", "--timeout", run.DefaultTimeoutS},
		[]string{"run", "--rounds", run.DefaultRounds},
		[]string{"loop", "--max-rounds", loop.DefaultMaxRounds},
		[]string{"loop", "--budget", loop.DefaultBudget},
	)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i][0] != rows[j][0] {
			return rows[i][0] < rows[j][0]
		}
		return rows[i][1] < rows[j][1]
	})
	return rows
}

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

func defaultsLine(cells []string, widths []int) string {
	var line strings.Builder
	line.WriteString("|")
	for i, text := range cells {
		line.WriteString(" " + text + strings.Repeat(" ", widths[i]-len(text)) + " |")
	}
	return line.String()
}

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
