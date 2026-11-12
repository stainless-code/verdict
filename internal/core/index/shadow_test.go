package indexcore

import "testing"

// TestImportQualifierShadowing checks calls through rebound imports.
func TestImportQualifierShadowing(t *testing.T) {
	tests := []struct {
		name   string
		lang   language
		source string
		want   int
	}{
		{"python import", langPython, "import m\nm.save(v)", 1},
		{"python assignment", langPython, "import m; m = Log()\nm.save(v)", 0},
		{"python local", langPython, "import m\ndef f():\n    m = Log()\n    m.save(v)", 0},
		{"python parameter", langPython, "import m\ndef f(m):\n    m.save(v)", 0},
		{"python for", langPython, "import m\nfor m in values:\n    m.save(v)", 0},
		{"python with", langPython, "import m\nwith context() as m:\n    m.save(v)", 0},
		{"python except", langPython, "import m\ntry:\n    work()\nexcept Error as m:\n    m.save(v)", 0},
		{"python sibling", langPython, "import m\ndef f(m):\n    m.save(v)\ndef g():\n    m.save(v)", 1},
		{"typescript import", langTypeScript, "import * as m from './saver'; m.save(v);", 1},
		{"typescript assignment", langTypeScript, "import * as m from './saver'; m = other; m.save(v);", 0},
		{"typescript parameter", langTypeScript, "import * as m from './saver'; function f(m) { m.save(v); }", 0},
		{"typescript let", langTypeScript, "import * as m from './saver'; function f() { let m = other; m.save(v); }", 0},
		{"typescript const", langTypeScript, "import * as m from './saver'; function f() { const m = other; m.save(v); }", 0},
		{"typescript var", langTypeScript, "import * as m from './saver'; function f() { var m = other; m.save(v); }", 0},
		{"typescript sibling", langTypeScript, "import * as m from './saver'; function f(m) { m.save(v); }\nfunction g() { m.save(v); }", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := scanShard("blob", []byte(tt.source), tt.lang, 100)
			f := &treeFile{Path: "uses", Language: string(tt.lang), Imports: s.Imports, Refs: s.Refs}
			got := queryCallers(map[string]*packageInfo{".": {dir: ".", files: []*treeFile{f}}}, []target{{pkg: &packageInfo{dir: "."}, name: "save", kind: wordFunc, lang: tt.lang}})
			if len(got.rows) != tt.want {
				t.Errorf("callers = %v; want %d rows", got.rows, tt.want)
			}
		})
	}
}
