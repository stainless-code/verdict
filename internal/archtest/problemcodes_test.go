package archtest

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	raw, err := os.ReadFile(filepath.Join(root, "skills", "verdict", "schemas", "envelope.schema.json"))
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
//
//nolint:revive // one AST walk over every package
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
		files := packages[dir]
		for _, file := range files {
			if strings.HasSuffix(fset.Position(file.Pos()).Filename, filepath.Join("internal", "core", "refusal.go")) {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				name, ok := problemCodeIdent(n)
				if !ok {
					return true
				}
				if code, ok := consts[name]; ok {
					sighted[code] = append(sighted[code], fset.Position(n.Pos()).String())
				}
				return true
			})
		}
	}
	return sighted
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
