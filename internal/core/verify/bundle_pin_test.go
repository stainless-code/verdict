package verifycore_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/testkit"
)

const (
	pinnedBundle = ".verdict/state/5201"
	otherBundle  = ".verdict/state/other"
)

// TestVerifyRefusesSymlinkedBundle pins that verify resolves its bundle like
// every writing verb: a .verdict/state/<pr> symlink refuses before anything
// runs, so no receipt lands where the link points.
func TestVerifyRefusesSymlinkedBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	testkit.WriteFile(t, filepath.Join(outside, "patch.json"), `{"verifyCmd":"printf ok"}`)
	if err := os.MkdirAll(".verdict/state", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, pinnedBundle); err != nil {
		t.Fatal(err)
	}
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}}
	_, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref == nil {
		t.Fatal("verify through a symlinked bundle succeeded, want a refusal")
	}
	if len(cmdr.got) != 0 {
		t.Errorf("refusal spawned %d commands", len(cmdr.got))
	}
	if _, err := os.Stat(filepath.Join(outside, "verify-receipt.json")); !os.IsNotExist(err) {
		t.Errorf("a receipt landed outside the repository (%v)", err)
	}
}

// TestVerifyBundleSwappedMidRun pins the bundle as verify opened it: a command
// that moves the bundle away and puts a symlink to another bundle, or a fresh
// directory, in its place leaves no receipt anywhere — not in the other
// bundle, not in the moved original (its earlier pass is dropped before the
// command starts) — and verify refuses.
func TestVerifyBundleSwappedMidRun(t *testing.T) {
	for _, tc := range []struct {
		replace func(t *testing.T)
		name    string
	}{
		{replaceWithSymlink, "symlink-to-other-bundle"},
		{replaceWithDirectory, "fresh-directory"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkSwapLeavesNoReceipt(t, tc.replace) })
	}
}

func replaceWithSymlink(t *testing.T) {
	t.Helper()
	if err := os.Symlink("other", pinnedBundle); err != nil {
		t.Fatal(err)
	}
}

func replaceWithDirectory(t *testing.T) {
	t.Helper()
	if err := os.Mkdir(pinnedBundle, 0o755); err != nil {
		t.Fatal(err)
	}
}

// trySwap is the hostile command's first move: rename the bundle away. On
// Windows the handle verify holds on the pinned directory makes the rename
// itself fail (a sharing violation), so the swap never happens; it reports
// that instead of failing, and any other platform must complete the rename.
func trySwap(t *testing.T, dir string) (blocked bool) {
	t.Helper()
	err := os.Rename(dir, dir+".moved")
	if err != nil && runtime.GOOS == "windows" {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	return false
}

// checkSwapLeavesNoReceipt runs verify with a command that moves the bundle
// away and calls replace in its place, then requires the refusal and no
// receipt in the other bundle, the moved original, or the replacement. Where
// the pinned handle blocks the rename (Windows), the bundle never moved: the
// other bundle still gets no receipt and no moved copy exists.
func checkSwapLeavesNoReceipt(t *testing.T, replace func(t *testing.T)) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, pinnedBundle+"/verify-receipt.json", receiptAt(""))
	testkit.WriteFile(t, otherBundle+"/patch.json", `{"verifyCmd":"printf ok"}`)
	blocked := false
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}, onRun: func() {
		if blocked = trySwap(t, pinnedBundle); !blocked {
			replace(t)
		}
	}}
	_, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if blocked {
		requireNoReceipt(t, otherBundle, pinnedBundle+".moved")
		return
	}
	if ref == nil || !strings.Contains(ref.Message, "changed while the command ran") {
		t.Fatalf("refusal = %v, want the changed-bundle refusal", ref)
	}
	requireNoReceipt(t, otherBundle, pinnedBundle+".moved", pinnedBundle)
}

// requireNoReceipt fails for every dir that holds a verify receipt.
func requireNoReceipt(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if _, err := os.Stat(dir + "/verify-receipt.json"); !os.IsNotExist(err) {
			t.Errorf("%s/verify-receipt.json exists (%v), want no receipt anywhere", dir, err)
		}
	}
}

// TestVerifyReceiptKeepsSigReadFirst pins that a command stripping findingSig
// from patch.json cannot widen its own receipt: the receipt names the finding
// verify read before the command ran.
func TestVerifyReceiptKeepsSigReadFirst(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok","findingSig":"c123456789abcdef"}`)
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}, onRun: func() {
		testkit.WriteFile(t, pinnedBundle+"/patch.json", `{"verifyCmd":"printf ok"}`)
	}}
	if _, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	checkReceiptFile(t, "c123456789abcdef")
}

// TestVerifyUnrecordableRunLeavesNoReceipt pins that a command corrupting
// patch.json refuses the record with no receipt left: the earlier pass was
// dropped before the command ran, so it cannot outlive this run.
func TestVerifyUnrecordableRunLeavesNoReceipt(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, pinnedBundle+"/verify-receipt.json", receiptAt(""))
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}, onRun: func() {
		testkit.WriteFile(t, pinnedBundle+"/patch.json", `not json`)
	}}
	_, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("refusal = %v, want corrupt-store", ref)
	}
	if _, err := os.Stat(pinnedBundle + "/verify-receipt.json"); !os.IsNotExist(err) {
		t.Errorf("an earlier passing receipt survived a run that could not record (%v)", err)
	}
}

// TestVerifyGroupBundleSwappedMidRun pins group validation the same way: a
// command that moves the bundle away and symlinks it to a directory outside
// the repository leaves no validation receipt there or in the moved original.
// On Windows the pinned handle blocks the rename, and nothing lands outside.
func TestVerifyGroupBundleSwappedMidRun(t *testing.T) {
	root := groupBundle(t)
	dir := filepath.Join(root, ".verdict", "state", "7")
	outside := t.TempDir()
	blocked := false
	cmdr := &groupCommander{out: "ok\n", onRun: func() {
		if blocked = trySwap(t, dir); !blocked {
			replaceWith(t, outside, dir)
		}
	}}
	_, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "go test ./...", cmdr))
	if !blocked && (ref == nil || !strings.Contains(ref.Message, "changed while the command ran")) {
		t.Fatalf("refusal = %v, want the changed-bundle refusal", ref)
	}
	requireNoGroupReceipt(t, outside, dir+".moved")
}

// replaceWith puts a symlink to target where the bundle was.
func replaceWith(t *testing.T, target, dir string) {
	t.Helper()
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
}

// requireNoGroupReceipt fails for every dir that holds the group receipt.
func requireNoGroupReceipt(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "validation-group-a1.json")); !os.IsNotExist(err) {
			t.Errorf("%s/validation-group-a1.json exists (%v), want no receipt written", d, err)
		}
	}
}

// TestVerifyRefusesEscapingPatchSymlink pins that patch.json is read through
// the pinned bundle: a patch.json symlinked to a file outside the bundle
// refuses before any command runs, never executing the verifyCmd it names.
func TestVerifyRefusesEscapingPatchSymlink(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := filepath.Join(t.TempDir(), "patch.json")
	testkit.WriteFile(t, outside, `{"verifyCmd":"printf ok"}`)
	if err := os.MkdirAll(pinnedBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, pinnedBundle+"/patch.json"); err != nil {
		t.Fatal(err)
	}
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}}
	_, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("refusal = %v, want corrupt-store", ref)
	}
	if len(cmdr.got) != 0 || cmdr.gits != 0 {
		t.Errorf("an escaping patch.json ran %d commands and %d git reads, want none", len(cmdr.got), cmdr.gits)
	}
}

// TestVerifyGroupTailSanitizesBeforeTheCut pins the group receipt's tail like
// the patch path's: the whole capture is sanitized before the cut, so a
// credential straddling the cut never leaves its second half in the tail.
func TestVerifyGroupTailSanitizesBeforeTheCut(t *testing.T) {
	root := groupBundle(t)
	const suffix = "secretsuffix1234567890"
	out := strings.Repeat("x", 100) + " sk-ant-api03-ZZ" + suffix + " " + strings.Repeat(".", 4000-len(suffix)-1)
	cmdr := &groupCommander{out: out}
	if _, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "go test ./...", cmdr)); ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	receipt, found, ref := verifycore.ReadValidationReceipt(filepath.Join(root, ".verdict", "state", "7"), "group-a1")
	if ref != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, ref)
	}
	if strings.Contains(receipt.OutputTail, suffix) {
		t.Fatalf("tail keeps the cut credential's second half: %q", receipt.OutputTail[:64])
	}
}

// TestVerifyGroupRefusesOtherContent pins deep-group validation to content:
// a checkout whose tree is not the reviewed commit's — uncommitted edits
// included — refuses before the command runs and writes no receipt.
func TestVerifyGroupRefusesOtherContent(t *testing.T) {
	root := groupBundle(t)
	cmdr := &groupCommander{out: "ok\n", worktree: "0d1ff0d1ff0d1ff0d1ff0d1ff0d1ff0d1ff0d1f"}
	_, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "go test ./...", cmdr))
	if ref == nil || ref.Code != "head-mismatch" || !strings.Contains(ref.Message, "is not the reviewed commit") {
		t.Fatalf("refusal = %v, want head-mismatch naming the reviewed commit", ref)
	}
	if len(cmdr.runs) != 0 {
		t.Errorf("a content mismatch ran %d commands", len(cmdr.runs))
	}
	requireNoGroupReceipt(t, filepath.Join(root, ".verdict", "state", "7"))
}

// TestReadValidationReceiptRejectsTrailingData pins the strict read: a
// passing group receipt followed by another value is unreadable, never the
// first value alone.
func TestReadValidationReceiptRejectsTrailingData(t *testing.T) {
	root := groupBundle(t)
	if _, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "go test ./...",
		&groupCommander{out: "ok\n"})); ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	path := filepath.Join(root, ".verdict", "state", "7", "validation-group-a1.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"passed":true}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, ref := verifycore.ReadValidationReceipt(filepath.Dir(path), "group-a1")
	if ref == nil || !strings.Contains(ref.Message, "trailing data after the receipt object") {
		t.Fatalf("read = %v, want the trailing-data refusal", ref)
	}
}
