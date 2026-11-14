package learncore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// vetoReason is a reason the retire and restore guards accept (30+ runes).
const vetoReason = "the rule stopped matching how this repo writes code"

// vetoSeed holds three active and three retired rules, so three retires and
// three restores each find their target.
const vetoSeed = `{"id":"active-one","rule":"Retire the first active rule","scope":"internal","provenance":"manual","rigor":"walked","status":"active"}
{"id":"active-two","rule":"Retire the second active rule","scope":"internal","provenance":"manual","rigor":"walked","status":"active"}
{"id":"active-three","rule":"Retire the third active rule","scope":"internal","provenance":"manual","rigor":"walked","status":"active"}
{"id":"retired-one","rule":"Restore the first retired rule","scope":"internal","provenance":"manual","rigor":"walked","status":"retired"}
{"id":"retired-two","rule":"Restore the second retired rule","scope":"internal","provenance":"manual","rigor":"walked","status":"retired"}
{"id":"retired-three","rule":"Restore the third retired rule","scope":"internal","provenance":"manual","rigor":"walked","status":"retired"}
`

// vetoSettle is how long the test watches for a write that ignores the lock it
// holds. A writer waiting on flock cannot land inside this window; the unlocked
// veto rewrote the store in it.
const vetoSettle = 200 * time.Millisecond

// mixedOp is one write started by the test, tagged with the row it must leave
// behind and a label for the failure message.
type mixedOp struct {
	req    *Request
	label  string
	wantID string
}

// TestVetoAndAppendKeepEveryRow proves an append and a veto share one lock:
// while the test holds the lock, neither kind of write lands, and once it is
// released twelve appends beside three retires and three restores each keep
// their row. Before the veto took the lock above its read, a retire or restore
// rewrote the rows it had read while the appends were still queued.
func TestVetoAndAppendKeepEveryRow(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	store := seedVetoStore(t, root)
	ops := mixedOps()
	held := holdStoreLock(t, store)
	refusals := startMixed(t, ops, held)
	for msg := range refusals {
		t.Error(msg)
	}
	rows := readStore(t, root)
	if len(rows) != 18 {
		t.Fatalf("rows after 12 appends and 6 vetoes = %d; want 18", len(rows))
	}
	for _, op := range ops {
		if kb.FindRow(rows, op.wantID) == nil {
			t.Errorf("%s lost its row %q", op.label, op.wantID)
		}
	}
	assertStatuses(t, rows)
}

// seedVetoStore writes the six seeded rows and returns the store path.
func seedVetoStore(t *testing.T, root string) string {
	t.Helper()
	store := filepath.Join(root, kb.File)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte(vetoSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	return store
}

// startMixed runs every op while the caller holds the store lock, fails the
// test if any op lands before it is released, then returns the refusal messages
// after all of them finish.
func startMixed(t *testing.T, ops []mixedOp, held func()) <-chan string {
	t.Helper()
	var wg sync.WaitGroup
	refusals := make(chan string, len(ops))
	returned := make(chan string, len(ops))
	for _, op := range ops {
		wg.Go(func() {
			_, ref := Run(context.Background(), op.req)
			if ref != nil {
				refusals <- fmt.Sprintf("%s: %s: %s", op.label, ref.Code, ref.Message)
			}
			returned <- op.label
		})
	}
	select {
	case label := <-returned:
		held()
		t.Fatalf("%s landed while another writer held the lock", label)
	case <-time.After(vetoSettle):
	}
	held()
	wg.Wait()
	close(refusals)
	return refusals
}

// readStore reads the store back as rows.
func readStore(t *testing.T, root string) []*jq.Obj {
	t.Helper()
	rows, err := kb.ReadRows(root)
	if err != nil {
		t.Fatalf("read the store back: %v", err)
	}
	return rows
}

// assertStatuses proves the three retires and three restores moved their rows
// and left the other three alone.
func assertStatuses(t *testing.T, rows []*jq.Obj) {
	t.Helper()
	for id, want := range map[string]string{
		"active-one":    "retired",
		"active-two":    "retired",
		"active-three":  "retired",
		"retired-one":   "active",
		"retired-two":   "active",
		"retired-three": "active",
	} {
		row := kb.FindRow(rows, id)
		if row == nil {
			t.Errorf("seeded row %q is gone", id)
			continue
		}
		if got := jq.Str(mustGet(row, "status")); got != want {
			t.Errorf("%s status = %q; want %q", id, got, want)
		}
	}
}

// appendPairs pairs each appended rule with the id its slug earns, spelled out
// so the expectation is not a second copy of the slug rule.
var appendPairs = []struct{ rule, id string }{
	{"Appended rule zero holds", "appended-rule-zero-holds"},
	{"Appended rule one holds", "appended-rule-one-holds"},
	{"Appended rule two holds", "appended-rule-two-holds"},
	{"Appended rule three holds", "appended-rule-three-holds"},
	{"Appended rule four holds", "appended-rule-four-holds"},
	{"Appended rule five holds", "appended-rule-five-holds"},
	{"Appended rule six holds", "appended-rule-six-holds"},
	{"Appended rule seven holds", "appended-rule-seven-holds"},
	{"Appended rule eight holds", "appended-rule-eight-holds"},
	{"Appended rule nine holds", "appended-rule-nine-holds"},
	{"Appended rule ten holds", "appended-rule-ten-holds"},
	{"Appended rule eleven holds", "appended-rule-eleven-holds"},
}

// mixedOps builds the eighteen writes the test starts together: twelve
// distinct appends, three retires, and three restores.
func mixedOps() []mixedOp {
	var ops []mixedOp
	for _, pair := range appendPairs {
		ops = append(ops, mixedOp{
			req:    writeRequest(&Request{Rule: pair.rule, Scope: "internal", Rigor: "walked"}),
			label:  "append " + pair.rule,
			wantID: pair.id,
		})
	}
	for _, id := range []string{"active-one", "active-two", "active-three"} {
		ops = append(ops, mixedOp{
			req:    writeRequest(&Request{Retire: id, Reason: vetoReason}),
			label:  "retire " + id,
			wantID: id,
		})
	}
	for _, id := range []string{"retired-one", "retired-two", "retired-three"} {
		ops = append(ops, mixedOp{
			req:    writeRequest(&Request{Restore: id, Reason: vetoReason}),
			label:  "restore " + id,
			wantID: id,
		})
	}
	return ops
}

// writeRequest arms one request with the runtime the service expects.
func writeRequest(req *Request) *Request {
	req.Runtime = &core.Runtime{}
	return req
}
