package verifycore_test

import (
	"context"
	"os"
	"path/filepath"
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

// checkSwapLeavesNoReceipt runs verify with a command that moves the bundle
// away and calls replace in its place, then requires the refusal and no
// receipt in the other bundle, the moved original, or the replacement.
func checkSwapLeavesNoReceipt(t *testing.T, replace func(t *testing.T)) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, pinnedBundle+"/verify-receipt.json", receiptAt(""))
	testkit.WriteFile(t, otherBundle+"/patch.json", `{"verifyCmd":"printf ok"}`)
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}, onRun: func() {
		if err := os.Rename(pinnedBundle, pinnedBundle+".moved"); err != nil {
			t.Fatal(err)
		}
		replace(t)
	}}
	_, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref == nil || !strings.Contains(ref.Message, "changed while the command ran") {
		t.Fatalf("refusal = %v, want the changed-bundle refusal", ref)
	}
	for _, dir := range []string{otherBundle, pinnedBundle + ".moved", pinnedBundle} {
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
