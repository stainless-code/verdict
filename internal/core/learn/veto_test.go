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
// holds. A writer waiting on the store lock — flock on Unix, LockFileEx on
// Windows — cannot land inside this window; the unlocked veto rewrote the store
// in it.
const vetoSettle = 200 * time.Millisecond

// vetoLockWait is the wait the veto test gives the store lock. Eighteen writers
// started together serialize on one fsync'd read-modify-write each, so a loaded
// machine can starve the last of them past the operator's five seconds while
// the code is right; the test grades the rows kept, not the shipped budget.
const vetoLockWait = time.Minute

// mixedOp is one write started by the test, tagged with the row it must leave
// behind and a label for the failure message.
type mixedOp struct {
	req    *Request
	label  string
	wantID string
}

// outcome is one write's return: the refusal it came back with, nil for a write
// that refused nothing, and its label.
type outcome struct {
	ref   *core.RefusalError
	label string
}

// TestVetoAndAppendKeepEveryRow proves an append and a veto share one lock:
// while the test holds the lock, neither kind of write lands, and once it is
// released twelve appends beside three retires and three restores each keep
// their row. Before the veto took the lock above its read, a retire or restore
// rewrote the rows it had read while the appends were still queued.
func TestVetoAndAppendKeepEveryRow(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Cleanup(kb.LockWaitForTest(vetoLockWait))
	store := seedVetoStore(t, root)
	ops := mixedOps()
	held := holdStoreLock(t, store)
	refusals := startMixed(t, ops, store, held)
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

// TestAppendRefusesInsideTheBudgetTheHookSets proves the seam the veto test
// raises reaches the shipped lock: with a one-nanosecond budget an append
// queued behind a held lock refuses store-busy inside the settle window, where
// the shipped five seconds is still waiting. It is the mutation guard on the
// hook itself: a hook nothing reads leaves this append blocked past the window.
func TestAppendRefusesInsideTheBudgetTheHookSets(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	store := seedVetoStore(t, root)
	release := holdStoreLock(t, store)
	defer release()
	t.Cleanup(kb.LockWaitForTest(time.Nanosecond))
	refused := make(chan *core.RefusalError, 1)
	go func() {
		_, ref := Run(context.Background(),
			writeRequest(&Request{Rule: appendPairs[0].rule, Scope: "internal", Rigor: "walked"}))
		refused <- ref
	}()
	select {
	case ref := <-refused:
		if ref == nil || ref.Code != codeStoreBusy {
			t.Fatalf("append under a held lock = %v; want a %s refusal", ref, codeStoreBusy)
		}
	case <-time.After(vetoSettle):
		t.Fatalf("append was still waiting %s into a one-nanosecond budget; the hook never "+
			"reached the store lock", vetoSettle)
	}
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
// test if any op returns before it is released, then returns the refusal
// messages after all of them finish. Two properties are watched, because a
// write that ignores the lock can show either one: an op that returns did not
// wait the window its held lock forces, and the store's bytes must not move
// while the lock is held, which catches a writer that writes before it waits.
// A returned op is reported by what it did — a nil refusal means it landed, and
// any refusal means it never waited, naming the refusal instead of claiming a
// landing the store never took.
func startMixed(t *testing.T, ops []mixedOp, store string, held func()) <-chan string {
	t.Helper()
	before := storeBytes(t, store)
	var wg sync.WaitGroup
	refusals := make(chan string, len(ops))
	returned := make(chan outcome, len(ops))
	for _, op := range ops {
		wg.Go(func() {
			_, ref := Run(context.Background(), op.req)
			if ref != nil {
				refusals <- fmt.Sprintf("%s: %s: %s", op.label, ref.Code, ref.Message)
			}
			returned <- outcome{label: op.label, ref: ref}
		})
	}
	select {
	case first := <-returned:
		held()
		t.Fatalf("%s returned while another writer held the lock: %s", first.label, earlyReturn(first))
	case <-time.After(vetoSettle):
	}
	if got := storeBytes(t, store); got != before {
		held()
		t.Fatalf("the store moved while another writer held the lock: %q", got)
	}
	held()
	wg.Wait()
	close(refusals)
	return refusals
}

// earlyReturn names what a write did before the lock was released: a write that
// refused nothing landed unlocked, and a write that refused something never
// waited its window, which is a pre-lock failure and never the store-busy a
// held lock earns after five seconds.
func earlyReturn(first outcome) string {
	if first.ref == nil {
		return "it landed"
	}
	return first.ref.Code + ": " + first.ref.Message
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
