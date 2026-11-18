package archtest

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestProblemCodesMatchPublishedEnum proves the codes the checker emits are
// exactly the enum published in envelope.schema.json and in the manual's
// problem table, in both directions.
//
//nolint:revive // the published-enum matrix reads as one block
func TestProblemCodesMatchPublishedEnum(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	codes := core.ProblemCodes()
	if len(codes) == 0 {
		t.Fatal("ProblemCodes returned nothing; the guard would pass vacuously")
	}
	if !slices.IsSorted(codes) || len(slices.Compact(slices.Clone(codes))) != len(codes) {
		t.Fatalf("ProblemCodes is not sorted and unique: %v", codes)
	}
	emitted := scanProblemCodes(t, root)
	for _, code := range slices.Sorted(maps.Keys(emitted)) {
		if !slices.Contains(codes, code) {
			t.Errorf("problem code %q is emitted at %s but missing from core.ProblemCodes()",
				code, strings.Join(emitted[code], ", "))
		}
	}
	for _, code := range codes {
		if !slices.Contains(slices.Sorted(maps.Keys(emitted)), code) {
			t.Errorf("published problem code %q has no emission site", code)
		}
	}
	checkProblemSchemaEnum(t, root, codes)
	checkProblemManualTable(t, root, codes)
}

// checkProblemSchemaEnum compares the generated schema enum with the registry.
func checkProblemSchemaEnum(t *testing.T, root string, codes []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "schemas", "envelope.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs struct {
			Problem struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"problem"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	enum := doc.Defs.Problem.Properties.Code.Enum
	if !slices.Equal(enum, codes) {
		t.Errorf("envelope.schema.json problem.code enum = %v, want %v", enum, codes)
	}
}

// checkProblemManualTable proves every published code has its documented row.
func checkProblemManualTable(t *testing.T, root string, codes []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "docs", "manual.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, code := range codes {
		level := core.ProblemCodeLevel(code)
		want := "`" + code + "`"
		found := slices.ContainsFunc(lines, func(line string) bool {
			cells := strings.Split(line, "|")
			// "mixed" marks a code whose level depends on whether record can
			// drop/move the part (warning) or must refuse it (error).
			return len(cells) >= 3 && strings.TrimSpace(cells[1]) == want &&
				(strings.TrimSpace(cells[2]) == level || strings.TrimSpace(cells[2]) == "mixed")
		})
		if !found {
			t.Errorf("docs/manual.md has no problem-table row for %s (%s)", want, level)
		}
	}
}

// scanProblemCodes returns every published problem code the tree references
// outside its own registry, by file:line. The code constants are the emission
// sites: CheckReview never spells the strings inline.
func scanProblemCodes(t *testing.T, root string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	packages := parsePackages(t, fset, root)
	consts := map[string]string{}
	for _, files := range packages {
		maps.Copy(consts, packageStringConsts(files))
	}
	sighted := map[string][]string{}
	for _, dir := range slices.Sorted(maps.Keys(packages)) {
		for _, file := range packages[dir] {
			if problemRegistry(fset, file) {
				continue
			}
			sightProblemCodes(fset, consts, file, sighted)
		}
	}
	return sighted
}

// problemRegistry reports whether file is the registry that defines the codes.
func problemRegistry(fset *token.FileSet, file *ast.File) bool {
	return strings.HasSuffix(fset.Position(file.Pos()).Filename, filepath.Join("internal", "core", "refusal.go"))
}

// sightProblemCodes records every published code one file names, by file:line.
func sightProblemCodes(fset *token.FileSet, consts map[string]string, file *ast.File, sighted map[string][]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			sightLiteral(fset, lit, sighted)
		}
		if name, ok := problemCodeIdent(n); ok {
			sightConst(fset, consts, name, n.Pos(), sighted)
		}
		return true
	})
}

// sightLiteral records a string literal that spells a published code.
func sightLiteral(fset *token.FileSet, lit *ast.BasicLit, sighted map[string][]string) {
	code, err := strconv.Unquote(lit.Value)
	if err != nil || !slices.Contains(core.ProblemCodes(), code) {
		return
	}
	sighted[code] = append(sighted[code], fset.Position(lit.Pos()).String())
}

// sightConst records a constant reference that names a published code.
func sightConst(fset *token.FileSet, consts map[string]string, name string, pos token.Pos, sighted map[string][]string) {
	code, ok := consts[name]
	if !ok {
		return
	}
	sighted[code] = append(sighted[code], fset.Position(pos).String())
}

// problemCodeIdent returns the identifier a ProblemCode* reference names.
func problemCodeIdent(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name, strings.HasPrefix(v.Name, "ProblemCode")
	case *ast.SelectorExpr:
		return v.Sel.Name, strings.HasPrefix(v.Sel.Name, "ProblemCode")
	default:
		return "", false
	}
}
