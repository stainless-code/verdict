package indexcore

import (
	"path"
	"slices"
	"strings"
)

// graphNode is one package directory in one language family. Go, Python, and
// the JavaScript family share no import edge, so they share no node: a Python
// import never makes a Go test reachable.
type graphNode struct {
	family language
	dir    string
}

// fileNode names the package one indexed file sits in.
func fileNode(f *treeFile) graphNode {
	return graphNode{family: fileLanguage(f).family(), dir: path.Dir(f.Path)}
}

// importGraph is a tree's reverse import edges, built once per query: for each
// package directory, the directories whose files import it.
type importGraph struct {
	byImportPath map[string]string
	dirs         map[string]bool
	edges        map[graphNode][]graphNode
}

// newImportGraph indexes every readable file's imports as reverse edges between
// package directories, resolved by each language's own specifier rules.
func newImportGraph(t *treeIndex) *importGraph {
	dirs := indexedDirs(t)
	g := &importGraph{dirs: dirs, byImportPath: importPathDirs(t.Module, dirs)}
	g.edges = g.reverseEdges(t)
	return g
}

// indexedDirs holds every package directory whose files the build parsed. An
// oversized or failed file declares no package anyone can import.
func indexedDirs(t *treeIndex) map[string]bool {
	dirs := make(map[string]bool, len(t.Files))
	for i := range t.Files {
		if f := &t.Files[i]; !f.unreadable() {
			dirs[path.Dir(f.Path)] = true
		}
	}
	return dirs
}

// unreadable reports an oversized or failed file: its imports were never
// recorded, so it contributes no edge.
func (f *treeFile) unreadable() bool {
	return f.Oversize || f.Error != ""
}

// reverseEdges collects, per imported directory, the directories that import
// it, sorted so a walk never depends on map iteration order.
func (g *importGraph) reverseEdges(t *treeIndex) map[graphNode][]graphNode {
	froms := make(map[graphNode]map[graphNode]bool)
	for i := range t.Files {
		if f := &t.Files[i]; !f.unreadable() {
			g.addFileEdges(froms, f)
		}
	}
	edges := make(map[graphNode][]graphNode, len(froms))
	for to, from := range froms {
		nodes := make([]graphNode, 0, len(from))
		for node := range from {
			nodes = append(nodes, node)
		}
		slices.SortFunc(nodes, compareNodes)
		edges[to] = nodes
	}
	return edges
}

// addFileEdges records one file's imports as reverse edges onto the packages it
// names.
func (g *importGraph) addFileEdges(froms map[graphNode]map[graphNode]bool, f *treeFile) {
	from := fileNode(f)
	for _, imp := range f.Imports {
		for _, to := range g.importTargets(fileLanguage(f), from.dir, imp) {
			if froms[to] == nil {
				froms[to] = make(map[graphNode]bool)
			}
			froms[to][from] = true
		}
	}
}

// importPathDirs renders one package directory's import path as a lookup, so a
// Go import names the directory it declares in.
func importPathDirs(module string, dirs map[string]bool) map[string]string {
	byPath := make(map[string]string, len(dirs))
	for dir := range dirs {
		byPath[importPathOf(module, dir)] = dir
	}
	return byPath
}

// compareNodes orders nodes by family then directory, so a walk's expansions
// never depend on map iteration order.
func compareNodes(a, b graphNode) int {
	if c := strings.Compare(string(a.family), string(b.family)); c != 0 {
		return c
	}
	return strings.Compare(a.dir, b.dir)
}

// importTargets resolves one import entry to the package directories it names:
// a Go import path, or a scanned specifier joined onto the importing file's
// directory, plus for Python the submodule a `from … import …` takes out of it.
func (g *importGraph) importTargets(lang language, dir string, imp importEntry) []graphNode {
	from := lang.family()
	if from == langGo {
		return g.addNodes(from, g.byImportPath[imp.Path])
	}
	module := scannedSpecifier(lang, dir, imp.Path)
	if module == "" {
		return nil
	}
	nodes := g.addNodes(from, g.moduleDir(module))
	if from == langPython && imp.Name != "" {
		nodes = append(nodes, g.addNodes(from, g.moduleDir(path.Join(module, imp.Name)))...)
	}
	return nodes
}

// moduleDir resolves one module path to the directory it sits in: the path
// itself for a package directory, otherwise its parent.
func (g *importGraph) moduleDir(module string) string {
	if g.dirs[module] {
		return module
	}
	return path.Dir(module)
}

// addNodes returns dir as a node of one family, or nothing when the tree holds
// no such directory.
func (g *importGraph) addNodes(family language, dir string) []graphNode {
	if dir == "" || !g.dirs[dir] {
		return nil
	}
	return []graphNode{{family: family, dir: dir}}
}

// reverseDepth labels every node with its import distance from the roots,
// walking at most limit hops, and returns the nodes one hop past the cap: they
// are counted, not walked.
func (g *importGraph) reverseDepth(roots []graphNode, limit int) (map[graphNode]int, []graphNode) {
	depth := make(map[graphNode]int, len(roots))
	frontier := g.seed(roots, depth)
	for hop := 1; hop <= limit && len(frontier) > 0; hop++ {
		frontier = g.expand(frontier, hop, depth)
	}
	beyond := g.expand(frontier, limit+1, depth)
	for _, node := range beyond {
		delete(depth, node)
	}
	return depth, beyond
}

// seed labels the roots at depth 0 and returns them as the first frontier.
func (g *importGraph) seed(roots []graphNode, depth map[graphNode]int) []graphNode {
	frontier := make([]graphNode, 0, len(roots))
	for _, root := range roots {
		if _, seen := depth[root]; seen {
			continue
		}
		depth[root] = 0
		frontier = append(frontier, root)
	}
	return frontier
}

// expand labels the nodes one hop outside the frontier with that hop and
// returns them as the next frontier. A node already labeled keeps the shorter
// distance, so a cycle terminates.
func (g *importGraph) expand(frontier []graphNode, hop int, depth map[graphNode]int) []graphNode {
	var next []graphNode
	for _, to := range frontier {
		for _, from := range g.edges[to] {
			if _, seen := depth[from]; seen {
				continue
			}
			depth[from] = hop
			next = append(next, from)
		}
	}
	return next
}
