package diagram

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func linearEdges(rows []File, added [][2]string) []Edge {
	freq, index := linearEdgeTargets(rows)
	seen := map[[2]int]bool{}
	var edges []Edge
	for _, add := range added {
		if cur, ok := index[add[0]]; ok {
			edges = appendLinearMentionEdges(edges, rows, freq, seen, cur, add[1])
		}
	}
	return edges
}

func appendLinearMentionEdges(
	edges []Edge, rows []File, freq map[string]int,
	seen map[[2]int]bool, cur int, line string,
) []Edge {
	for j, row := range rows {
		key := [2]int{cur, j}
		if j == cur || seen[key] || !linearMentions(line, row.Path, freq[linearBaseName(row.Path)] == 1) {
			continue
		}
		seen[key] = true
		edges = append(edges, Edge{
			From:  rows[cur].ID,
			To:    row.ID,
			Label: linearEdgeLabel(&rows[cur], &rows[j], freq),
		})
	}
	return edges
}

func linearEdgeTargets(rows []File) (map[string]int, map[string]int) {
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[linearBaseName(row.Path)]++
		index[row.Path] = i
	}
	return freq, index
}

func linearEdgeLabel(src, dst *File, freq map[string]int) string {
	target := dst.Path
	if freq[linearBaseName(dst.Path)] == 1 {
		target = linearBaseName(dst.Path)
	}
	relation := "uses "
	if src.Role == "test" || src.Role == "config" {
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

type topologyEquivalenceCase struct {
	name  string
	rows  []File
	added [][2]string
}

func topologyEquivalenceCases() []topologyEquivalenceCase {
	return []topologyEquivalenceCase{
		{name: "empty", rows: nil, added: nil},
		{
			name: "empty path row",
			rows: []File{{ID: "F1", Path: ""}, {ID: "F2", Path: "a.txt"}},
			added: [][2]string{
				{"a.txt", "anything"},
				{"a.txt", ""},
				{"", "a.txt"},
			},
		},
		{
			name: "duplicate paths",
			rows: []File{{ID: "F1", Path: "p/a.go"}, {ID: "F2", Path: "p/a.go"}, {ID: "F3", Path: "q"}},
			added: [][2]string{
				{"p/a.go", "see p/a.go"},
				{"p/a.go", "see p/a.go and q"},
			},
		},
		{
			name: "shared basename",
			rows: []File{{ID: "F1", Path: "x/a.go"}, {ID: "F2", Path: "y/a.go"}},
			added: [][2]string{
				{"x/a.go", "a.go"},
				{"x/a.go", "x/a.go"},
				{"y/a.go", "y/a.go"},
			},
		},
		{
			name: "unique basename",
			rows: []File{{ID: "F1", Path: "x/b.go"}, {ID: "F2", Path: "c.go"}},
			added: [][2]string{
				{"x/b.go", "b.go"},
				{"x/b.go", "x/b.go"},
				{"c.go", "prefixc.gosuffix"},
			},
		},
		{
			name: "trailing slash empty base",
			rows: []File{{ID: "F1", Path: "dir/"}, {ID: "F2", Path: "file.go"}},
			added: [][2]string{
				{"dir/", "anything at all"},
				{"file.go", "file.go"},
			},
		},
		{
			name: "shared empty base",
			rows: []File{{ID: "F1", Path: "one/"}, {ID: "F2", Path: "two/"}},
			added: [][2]string{
				{"one/", "anything at all"},
				{"one/", "one/"},
				{"one/", "two/"},
			},
		},
		{
			name: "unicode",
			rows: []File{{ID: "F1", Path: "pkg/é.txt"}, {ID: "F2", Path: "pkg/ß.txt"}},
			added: [][2]string{
				{"pkg/é.txt", "é.txt"},
				{"pkg/ß.txt", "pkg/ß.txt"},
			},
		},
		{
			name: "seen dedupes per source target pair",
			rows: []File{{ID: "F1", Path: "a.go"}, {ID: "F2", Path: "b.go"}},
			added: [][2]string{
				{"a.go", "b.go and b.go"},
				{"a.go", "a.go and b.go"},
				{"b.go", "a.go and b.go"},
			},
		},
		{
			name: "cur missing",
			rows: []File{{ID: "F1", Path: "a.go"}},
			added: [][2]string{
				{"missing.go", "a.go"},
				{"a.go", "a.go"},
			},
		},
		{
			name: "no slash",
			rows: []File{{ID: "F1", Path: "Makefile"}, {ID: "F2", Path: "make"}},
			added: [][2]string{
				{"Makefile", "make Makefile"},
				{"make", "Makefile"},
			},
		},
		{
			name: "path is substring of another path",
			rows: []File{{ID: "F1", Path: "a.go"}, {ID: "F2", Path: "aa.go"}, {ID: "F3", Path: "aaa.go"}},
			added: [][2]string{
				{"a.go", "aaa.go"},
				{"aaa.go", "a.go"},
				{"aa.go", "aaaa.go"},
			},
		},
		{
			name: "overlapping patterns",
			rows: []File{{ID: "F1", Path: "f.txt"}, {ID: "F2", Path: "x/f.txt"}, {ID: "F3", Path: "x/f.txt.bak"}},
			added: [][2]string{
				{"f.txt", "x/f.txt.bak"},
				{"x/f.txt", "x/f.txt.bak and f.txt"},
			},
		},
		{
			name: "repeated source lines",
			rows: []File{
				{ID: "F1", Path: "src/a.go", Role: "test"},
				{ID: "F2", Path: "src/b.go", Role: "config"},
				{ID: "F3", Path: "src/c.go", Role: "src"},
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

// TestEdgesMatchLinearScan pins the indexed scan to the linear
// implementation it replaced, including the empty-path and hash-edge cases.
func TestEdgesMatchLinearScan(t *testing.T) {
	for _, tc := range topologyEquivalenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := Edges(tc.rows, tc.added)
			want := linearEdges(tc.rows, tc.added)
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

func randomEquivalenceRows(rng *equivalenceRand) []File {
	roles := []string{"src", "test", "config"}
	rows := make([]File, rng.intN(20))
	for i := range rows {
		rows[i] = File{
			ID:   "F" + strconv.Itoa(i+1),
			Path: randomEquivalencePath(rng),
			Role: roles[rng.intN(len(roles))],
		}
	}
	return rows
}

func randomEquivalenceLine(rng *equivalenceRand, rows []File) string {
	switch rng.intN(6) {
	case 0:
		return ""
	case 1:
		return "nothing here"
	case 2:
		return randomEquivalenceWord(rng)
	case 3:
		if len(rows) > 0 {
			return "see " + linearBaseName(rows[rng.intN(len(rows))].Path) + " here"
		}
	}
	var sb strings.Builder
	for range rng.intN(5) {
		if len(rows) > 0 && rng.intN(3) == 0 {
			_, _ = sb.WriteString(rows[rng.intN(len(rows))].Path)
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

func randomEquivalenceAdded(rng *equivalenceRand, rows []File) [][2]string {
	added := make([][2]string, rng.intN(20))
	for i := range added {
		path := "missing/go.go"
		if len(rows) > 0 && rng.intN(4) != 0 {
			path = rows[rng.intN(len(rows))].Path
		}
		added[i] = [2]string{path, randomEquivalenceLine(rng, rows)}
	}
	return added
}

// TestEdgesIndexedScan covers the rolling-hash path selected once the
// pattern count passes the linear threshold.
func TestEdgesIndexedScan(t *testing.T) {
	rows := make([]File, 40)
	for i := range rows {
		rows[i] = File{
			ID:   "F" + strconv.Itoa(i+1),
			Path: "pkg/d" + strconv.Itoa(i%5) + "/f" + strconv.Itoa(i) + ".go",
		}
	}
	rows[0].Path = "pkg/d0/shared.go"
	rows[10].Path = "pkg/d1/shared.go"
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
	got := Edges(rows, added)
	want := linearEdges(rows, added)
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

// TestEdgesRandomizedEquivalence fuzzes paths, roles, and lines with a
// fixed seed so failures reproduce.
func TestEdgesRandomizedEquivalence(t *testing.T) {
	rng := &equivalenceRand{state: 1}
	for i := range 300 {
		rows := randomEquivalenceRows(rng)
		added := randomEquivalenceAdded(rng, rows)
		got := Edges(rows, added)
		want := linearEdges(rows, added)
		if !slices.Equal(got, want) {
			t.Fatalf("case %d rows=%v added=%v\n got: %v\nwant: %v", i, rows, added, got, want)
		}
	}
}
