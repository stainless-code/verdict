package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
)

// The runes encoding/json writes raw but a terminal obeys: two C1 controls
// (U+009B is a CSI) and two bidi controls. They are built from code points so
// no editor or tool rewrites the source.
var controlRunes = []rune{0x85, 0x9B, 0x202E, 0x2066}

// hostileBody is a finding body that carries every control rune.
func hostileBody() string {
	return "Usable body" + string(controlRunes[0]) + " here" + string(controlRunes[1]) +
		"[2J with enough" + string(controlRunes[2]) + " substance" + string(controlRunes[3]) + " to post."
}

// escaped is the lowercase `\uXXXX` spelling the wire must carry for r.
func escaped(r rune) string { return fmt.Sprintf("%cu%04x", '\\', r) }

// assertNoRawControls fails when wire carries a control rune unescaped.
func assertNoRawControls(t *testing.T, wire string) {
	t.Helper()
	for _, r := range controlRunes {
		if strings.ContainsRune(wire, r) {
			t.Errorf("response carries the raw rune %U", r)
		}
	}
}

// TestResponseEscapesC1AndBidiControls proves a finding's PR-derived text
// reaches a serve response with the controls escaped and decodes to the same
// string.
func TestResponseEscapesC1AndBidiControls(t *testing.T) {
	t.Parallel()
	files := fixtureFiles()
	for rel, content := range files {
		files[rel] = strings.ReplaceAll(content, "Usable body here with enough substance.", hostileBody())
	}
	root := writeFixture(t, files)
	ts := testServer(t, testLayer(root, nil))
	rootID := query.RootID(mustAbs(t, root))
	path := "/v1/roots/" + rootID + "/bundles/1234/findings/" + sigCrit
	got := call(t, ts, http.MethodGet, path, "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("GET %s = %d %s; want 200", path, got.status, got.body)
	}
	wire := string(got.body)
	assertNoRawControls(t, wire)
	for _, r := range controlRunes {
		if !strings.Contains(wire, escaped(r)) {
			t.Errorf("response lacks the escape %s", escaped(r))
		}
	}
	if !decodedHasBody(t, decode(t, got.body)) {
		t.Errorf("decoded response lost the body %q", hostileBody())
	}
}

// TestProblemBodyEscapesControls covers the transport-error writer, which
// marshals its own body rather than rendering an envelope.
func TestProblemBodyEscapesControls(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	(&Server{}).problem(rec, http.StatusBadRequest, "bad-input", "name"+string(controlRunes[1])+string(controlRunes[2]))
	wire := rec.Body.String()
	assertNoRawControls(t, wire)
	if want := `"message":"name` + escaped(controlRunes[1]) + escaped(controlRunes[2]) + `"`; !strings.Contains(wire, want) {
		t.Errorf("problem body = %q; want it to contain %s", wire, want)
	}
}

// decodedHasBody reports whether the decoded document carries the hostile
// body as one string value (re-encoding leaves the control runes raw, so the
// encoded body is a substring exactly when some string equals it).
func decodedHasBody(t *testing.T, v any) bool {
	t.Helper()
	doc, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(hostileBody())
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(doc, body)
}
