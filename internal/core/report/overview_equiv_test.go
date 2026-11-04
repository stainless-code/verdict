package reportcore

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func linearOverviewEdges(rows []fileRowShape, added [][2]string) []overviewEdge {
	freq, index := linearEdgeTargets(rows)
	seen := map[[2]int]bool{}
	var edges []overviewEdge
	for _, add := range added {
		if cur, ok := index[add[0]]; ok {
			edges = appendLinearMentionEdges(edges, rows, freq, seen, cur, add[1])
		}
	}
	return edges
}

func appendLinearMentionEdges(
	edges []overviewEdge, rows []fileRowShape, freq map[string]int,
	seen map[[2]int]bool, cur int, line string,
) []overviewEdge {
	for j, row := range rows {
		key := [2]int{cur, j}
		if j == cur || seen[key] || !linearMentions(line, row.path, freq[linearBaseName(row.path)] == 1) {
			continue
		}
		seen[key] = true
		edges = append(edges, overviewEdge{
			from:  rows[cur].id,
			to:    row.id,
			label: linearEdgeLabel(rows[cur], row, freq),
		})
	}
	return edges
}

func linearEdgeTargets(rows []fileRowShape) (map[string]int, map[string]int) {
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[linearBaseName(row.path)]++
		index[row.path] = i
	}
	return freq, index
}

func linearEdgeLabel(src, dst fileRowShape, freq map[string]int) string {
	target := dst.path
	if freq[linearBaseName(dst.path)] == 1 {
		target = linearBaseName(dst.path)
	}
	relation := "uses "
	if src.role == "test" || src.role == "config" {
		relation = "validates "
	}
	return relation + target
}

func linearMentions(line, path string, uniqueBase bool) bool {
	if uniqueBase && strings.Contains(line, linearBaseName(path)) {
		return true
	}
	return strings.Contains(line, path)
}

func linearBaseName(path string) string {
	if _, after, ok := strings.CutLast(path, "/"); ok {
		return after
	}
	return path
}

type overviewEquivalenceCase struct {
	name  string
	rows  []fileRowShape
	added [][2]string
}

func overviewEquivalenceCases() []overviewEquivalenceCase {
	return []overviewEquivalenceCase{
		{name: "empty", rows: nil, added: nil},
		{
			name: "empty path row",
			rows: []fileRowShape{{id: "F1", path: ""}, {id: "F2", path: "a.txt"}},
			added: [][2]string{
				{"a.txt", "anything"},
				{"a.txt", ""},
				{"", "a.txt"},
			},
		},
		{
			name: "duplicate paths",
			rows: []fileRowShape{{id: "F1", path: "p/a.go"}, {id: "F2", path: "p/a.go"}, {id: "F3", path: "q"}},
			added: [][2]string{
				{"p/a.go", "see p/a.go"},
				{"p/a.go", "see p/a.go and q"},
			},
		},
		{
			name: "shared basename",
			rows: []fileRowShape{{id: "F1", path: "x/a.go"}, {id: "F2", path: "y/a.go"}},
			added: [][2]string{
				{"x/a.go", "a.go"},
				{"x/a.go", "x/a.go"},
				{"y/a.go", "y/a.go"},
			},
		},
		{
			name: "unique basename",
			rows: []fileRowShape{{id: "F1", path: "x/b.go"}, {id: "F2", path: "c.go"}},
			added: [][2]string{
				{"x/b.go", "b.go"},
				{"x/b.go", "x/b.go"},
				{"c.go", "prefixc.gosuffix"},
			},
		},
		{
			name: "trailing slash empty base",
			rows: []fileRowShape{{id: "F1", path: "dir/"}, {id: "F2", path: "file.go"}},
			added: [][2]string{
				{"dir/", "anything at all"},
				{"file.go", "file.go"},
			},
		},
		{
			name: "shared empty base",
			rows: []fileRowShape{{id: "F1", path: "one/"}, {id: "F2", path: "two/"}},
			added: [][2]string{
				{"one/", "anything at all"},
				{"one/", "one/"},
				{"one/", "two/"},
			},
		},
		{
			name: "unicode",
			rows: []fileRowShape{{id: "F1", path: "pkg/é.txt"}, {id: "F2", path: "pkg/ß.txt"}},
			added: [][2]string{
				{"pkg/é.txt", "é.txt"},
				{"pkg/ß.txt", "pkg/ß.txt"},
			},
		},
		{
			name: "seen dedupes per source target pair",
			rows: []fileRowShape{{id: "F1", path: "a.go"}, {id: "F2", path: "b.go"}},
			added: [][2]string{
				{"a.go", "b.go and b.go"},
				{"a.go", "a.go and b.go"},
				{"b.go", "a.go and b.go"},
			},
		},
		{
			name: "cur missing",
			rows: []fileRowShape{{id: "F1", path: "a.go"}},
			added: [][2]string{
				{"missing.go", "a.go"},
				{"a.go", "a.go"},
			},
		},
		{
			name: "no slash",
			rows: []fileRowShape{{id: "F1", path: "Makefile"}, {id: "F2", path: "make"}},
			added: [][2]string{
				{"Makefile", "make Makefile"},
				{"make", "Makefile"},
			},
		},
		{
			name: "path is substring of another path",
			rows: []fileRowShape{{id: "F1", path: "a.go"}, {id: "F2", path: "aa.go"}, {id: "F3", path: "aaa.go"}},
			added: [][2]string{
				{"a.go", "aaa.go"},
				{"aaa.go", "a.go"},
				{"aa.go", "aaaa.go"},
			},
		},
		{
			name: "overlapping patterns",
			rows: []fileRowShape{{id: "F1", path: "f.txt"}, {id: "F2", path: "x/f.txt"}, {id: "F3", path: "x/f.txt.bak"}},
			added: [][2]string{
				{"f.txt", "x/f.txt.bak"},
				{"x/f.txt", "x/f.txt.bak and f.txt"},
			},
		},
		{
			name: "repeated source lines",
			rows: []fileRowShape{
				{id: "F1", path: "src/a.go", role: "test"},
				{id: "F2", path: "src/b.go", role: "config"},
				{id: "F3", path: "src/c.go", role: "src"},
			},
			added: [][2]string{
				{"src/a.go", "b.go"},
				{"src/a.go", "b.go"},
				{"src/b.go", "c.go"},
				{"src/c.go", "a.go"},
			},
		},
	}
}

// TestOverviewEdgesMatchLinearScan pins the indexed scan to the linear
// implementation it replaced, including the empty-path and hash-edge cases.
func TestOverviewEdgesMatchLinearScan(t *testing.T) {
	for _, tc := range overviewEquivalenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := overviewEdges(tc.rows, tc.added)
			want := linearOverviewEdges(tc.rows, tc.added)
			if !slices.Equal(got, want) {
				t.Fatalf("edges differ\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

var equivalenceBaseNames = []string{"a.go", "b.go", "c.go", "f1.txt", "f2.txt", "z", ""}

func randomEquivalencePath(rng *equivalenceRand) string {
	switch rng.intN(6) {
	case 0:
		return ""
	case 1:
		return equivalenceBaseNames[rng.intN(len(equivalenceBaseNames))]
	case 2:
		return "pkg/" + equivalenceBaseNames[rng.intN(len(equivalenceBaseNames))]
	case 3:
		return randomEquivalenceWord(rng) + "/" + randomEquivalenceWord(rng) + ".go"
	case 4:
		return "dir/"
	default:
		return randomEquivalenceWord(rng)
	}
}

func randomEquivalenceWord(rng *equivalenceRand) string {
	const alphabet = "ab/f.g"
	var sb strings.Builder
	for range rng.intN(6) {
		_ = sb.WriteByte(alphabet[rng.intN(len(alphabet))])
	}
	return sb.String()
}

func randomEquivalenceRows(rng *equivalenceRand) []fileRowShape {
	roles := []string{"src", "test", "config"}
	rows := make([]fileRowShape, rng.intN(20))
	for i := range rows {
		rows[i] = fileRowShape{
			id:   "F" + strconv.Itoa(i+1),
			path: randomEquivalencePath(rng),
			role: roles[rng.intN(len(roles))],
		}
	}
	return rows
}

func randomEquivalenceLine(rng *equivalenceRand, rows []fileRowShape) string {
	switch rng.intN(6) {
	case 0:
		return ""
	case 1:
		return "nothing here"
	case 2:
		return randomEquivalenceWord(rng)
	case 3:
		if len(rows) > 0 {
			return "see " + linearBaseName(rows[rng.intN(len(rows))].path) + " here"
		}
	}
	var sb strings.Builder
	for range rng.intN(5) {
		if len(rows) > 0 && rng.intN(3) == 0 {
			_, _ = sb.WriteString(rows[rng.intN(len(rows))].path)
			continue
		}
		_, _ = sb.WriteString(randomEquivalenceWord(rng))
	}
	return sb.String()
}

// equivalenceRand is a deterministic splitmix64 generator; the test only
// needs reproducible choices, not the rand package's API surface.
type equivalenceRand struct{ state uint64 }

func (r *equivalenceRand) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *equivalenceRand) intN(n int) int {
	return int(r.next() % uint64(n))
}

func randomEquivalenceAdded(rng *equivalenceRand, rows []fileRowShape) [][2]string {
	added := make([][2]string, rng.intN(20))
	for i := range added {
		path := "missing/go.go"
		if len(rows) > 0 && rng.intN(4) != 0 {
			path = rows[rng.intN(len(rows))].path
		}
		added[i] = [2]string{path, randomEquivalenceLine(rng, rows)}
	}
	return added
}

// TestOverviewEdgesIndexedScan covers the rolling-hash path selected once the
// pattern count passes the linear threshold.
func TestOverviewEdgesIndexedScan(t *testing.T) {
	rows := make([]fileRowShape, 40)
	for i := range rows {
		rows[i] = fileRowShape{
			id:   "F" + strconv.Itoa(i+1),
			path: "pkg/d" + strconv.Itoa(i%5) + "/f" + strconv.Itoa(i) + ".go",
		}
	}
	rows[0].path = "pkg/d0/shared.go"
	rows[10].path = "pkg/d1/shared.go"
	added := [][2]string{
		{"pkg/d0/shared.go", "see f1.go and f2.go"},
		{"pkg/d1/f1.go", "pkg/d2/f2.go then pkg/d1/f1.go"},
		{"pkg/d2/f2.go", "f39.go and f38.go and f39.go"},
		{"pkg/d3/f3.go", "prefixf4.gosuffix"},
		{"pkg/d4/f4.go", "shared.go"},
		{"pkg/d0/shared.go", "pkg/d1/shared.go"},
		{"pkg/d6/f6.go", "f0.go"},
		{"pkg/d7/f7.go", ""},
	}
	got := overviewEdges(rows, added)
	want := linearOverviewEdges(rows, added)
	if !slices.Equal(got, want) {
		t.Fatalf("edges differ\n got: %v\nwant: %v", got, want)
	}
}

// TestMentionIndexHashCollisions proves a shared hash is disambiguated by the
// pattern text instead of trusted on its own.
func TestMentionIndexHashCollisions(t *testing.T) {
	m := &mentionIndex{
		names:  []string{"aa", "bb"},
		rows:   []int{0, 1},
		starts: []int{0, 1, 2},
		slots:  []mentionEntry{{hash: 4, id: 0}, {hash: 4, id: 1}, {id: -1}, {id: -1}},
		mask:   3,
	}
	for _, tc := range []struct {
		window string
		want   []int
	}{
		{window: "aa", want: []int{0}},
		{window: "bb", want: []int{1}},
		{window: "cc"},
	} {
		if got := m.appendWindow(4, tc.window, nil); !slices.Equal(got, tc.want) {
			t.Errorf("appendWindow(%q) = %v, want %v", tc.window, got, tc.want)
		}
	}
}

// TestOverviewEdgesRandomizedEquivalence fuzzes paths, roles, and lines with a
// fixed seed so failures reproduce.
func TestOverviewEdgesRandomizedEquivalence(t *testing.T) {
	rng := &equivalenceRand{state: 1}
	for i := range 300 {
		rows := randomEquivalenceRows(rng)
		added := randomEquivalenceAdded(rng, rows)
		got := overviewEdges(rows, added)
		want := linearOverviewEdges(rows, added)
		if !slices.Equal(got, want) {
			t.Fatalf("case %d rows=%v added=%v\n got: %v\nwant: %v", i, rows, added, got, want)
		}
	}
}
