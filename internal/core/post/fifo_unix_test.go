//go:build unix

package postcore

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// fifoDeadline bounds a Run that must refuse at once: every bundle read opens
// non-blocking, so a Run still going here regressed to a plain open.
const fifoDeadline = 5 * time.Second

// runWithFIFO swaps one bundle input for a FIFO and dry-runs post under
// [fifoDeadline]. A Run still going at the deadline regressed to a plain open:
// the blocked reader is released and joined, then the test fails.
func runWithFIFO(t *testing.T, name string) *core.RefusalError {
	t.Helper()
	t.Chdir(t.TempDir())
	postBundle(t, postReview)
	fifo := ".verdict/state/7/" + name
	if err := os.Remove(fifo); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	done := make(chan *core.RefusalError, 1)
	go func() {
		var warnings, human []string
		req := postTestReq(postCommander(t), Flags{
			Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
		}, &warnings, &human)
		_, ref := Run(context.Background(), req)
		done <- ref
	}()
	select {
	case ref := <-done:
		return ref
	case <-time.After(fifoDeadline):
		releaseFIFO(t, fifo, done)
		t.Fatalf("Run blocked on %s as a FIFO", name)
		return nil
	}
}

// fifoRefusalCode is the refusal's code, "" for no refusal.
func fifoRefusalCode(ref *core.RefusalError) string {
	if ref == nil {
		return ""
	}
	return ref.Code
}

// TestRunRefusesAFIFOBundleInputWithoutBlocking pins the shared readers: a
// local process that swaps a bundle file for a FIFO after fetch must get a
// prompt typed refusal (or a clean run when the input is optional), never a
// Run that waits for a writer forever.
//
// Break killed: reading bundle JSON through a plain [os.Open] makes every leg
// wait for a writer and the deadline fires.
func TestRunRefusesAFIFOBundleInputWithoutBlocking(t *testing.T) {
	// want is the refusal code the swapped input yields; "" means the input is
	// optional and the dry run completes without it.
	for name, want := range map[string]string{
		"PR.json": "controls-stale", "full.diff": "missing-diff", "record.json": "", "review.json": "missing-review-file",
	} {
		t.Run(name, func(t *testing.T) {
			ref := runWithFIFO(t, name)
			if got := fifoRefusalCode(ref); got != want {
				t.Fatalf("Run with %s as a FIFO = %v; want refusal code %q", name, ref, want)
			}
		})
	}
}

// releaseFIFO unblocks a reader stuck in open(2) by opening the FIFO for
// writing, then joins the worker so none leaks behind a failed test.
func releaseFIFO[T any](t *testing.T, fifo string, done <-chan T) {
	t.Helper()
	deadline := time.After(fifoDeadline)
	for {
		if fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		select {
		case <-done:
			return
		case <-deadline:
			t.Error("the blocked worker could not be released and is leaked")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRunReadsAnOperatorReviewFileFromAFIFO pins the other half of the rule: a
// path the operator names with --review-file keeps normal read semantics, so a
// pipe with a writer (the shape of /dev/stdin and <(cmd)) is read, not refused
// as not-regular. The bundle's own review.json, by contrast, is refused without
// blocking (see the review.json leg above).
//
// Break killed: routing the operator path through noblock refuses the FIFO as
// not-regular and the dry run fails.
func TestRunReadsAnOperatorReviewFileFromAFIFO(t *testing.T) {
	t.Chdir(t.TempDir())
	postBundle(t, postReview)
	fifo := "operator-review.fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	writeErr := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			writeErr <- err
			return
		}
		_, err = w.WriteString(testenv.MaybeFindingsReview("review.json", postReview))
		writeErr <- errors.Join(err, w.Close())
	}()
	done := make(chan *core.RefusalError, 1)
	go func() {
		var warnings, human []string
		req := postTestReq(postCommander(t), Flags{Approve: "false", ReviewFile: fifo, DryRun: true}, &warnings, &human)
		_, ref := Run(context.Background(), req)
		done <- ref
	}()
	select {
	case ref := <-done:
		if ref != nil {
			t.Fatalf("Run with --review-file as a FIFO = %v; want a clean dry run", ref)
		}
	case <-time.After(fifoDeadline):
		releaseFIFO(t, fifo, done)
		t.Fatal("Run blocked reading an operator --review-file FIFO")
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("FIFO writer: %v", err)
	}
}
