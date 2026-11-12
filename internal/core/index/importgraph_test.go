package indexcore

import "testing"

// TestTestsForCrossFamilyModuleIsNoEdge proves a module exists only in its
// language family: a TypeScript specifier naming a Python file creates no edge.
func TestTestsForCrossFamilyModuleIsNoEdge(t *testing.T) {
	f := newFixture(t)
	f.write("lib/save.ts", "export function save(id: string): void {}\n")
	f.write("lib/only.py", "def save(value):\n    return value\n")
	f.write("tests/deep.spec.ts", "import { save } from '../lib/only';\n\nsave(1);\n")
	f.commit("one")
	f.build()
	assertTestRows(t, f.queryPath(QueryTestsFor, "lib/save.ts").Data.TestRows, nil)
}
