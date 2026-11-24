//go:build unix

package serve

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

const (
	// fifoDeadline bounds a call that must return at once: a non-blocking open
	// of a FIFO answers immediately, so a call still running here regressed to
	// a blocking open.
	fifoDeadline = 5 * time.Second
	// fifoReleasePoll paces the writer opens that release a blocked reader.
	fifoReleasePoll = 10 * time.Millisecond
)

// withinDeadline runs call under [fifoDeadline] and returns its result. A call
// that outlives it regressed to a blocking open of fifo: the test releases the
// blocked reader by opening the FIFO for writing (which makes the reader's
// open return), joins the worker, and only then fails, so no goroutine is left
// blocked behind a failed test.
func withinDeadline(t *testing.T, fifo string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(fifoDeadline):
	}
	release := time.NewTimer(fifoDeadline)
	defer release.Stop()
	for {
		if fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		select {
		case err := <-done:
			t.Fatalf("the call blocked on a FIFO: it waited for a writer on a non-regular file (then returned %v)", err)
			return nil
		case <-release.C:
			t.Fatal("the call blocked on a FIFO and could not be released; its worker is leaked")
			return nil
		case <-time.After(fifoReleasePoll):
		}
	}
}

func fifoAt(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
}

func notRegular(err error) bool {
	return err != nil && strings.Contains(err.Error(), readcommon.ErrNotRegular.Error())
}

// TestBundleFingerprintRefusesEveryFIFOInputWithoutBlocking pins the
// non-blocking guard on every input the fingerprint reads, not the diff alone:
// a bundle file that is a FIFO with no writer would block a plain open forever
// while the findings-store lock is held, past the submit deadline and server
// shutdown. The open itself is non-blocking and the opened descriptor is what
// is refused, so there is no stat-then-open window for a swap to land in.
func TestBundleFingerprintRefusesEveryFIFOInputWithoutBlocking(t *testing.T) { //nolint:revive // one loop over every input
	t.Parallel()
	for _, name := range append(append([]string{}, fingerprintFiles...), diffFile) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			fifoAt(t, path)
			err := withinDeadline(t, path, func() error {
				_, err := bundleFingerprint(dir)
				return err
			})
			if !notRegular(err) || !strings.Contains(err.Error(), name) {
				t.Fatalf("bundleFingerprint = %v, want a not-a-regular-file refusal naming %s", err, name)
			}
		})
	}
}

// TestBundleFingerprintRefusesASymlinkedFIFOWithoutBlocking proves the
// descriptor check covers a link whose target is swapped to a FIFO: following
// the link must not wait for a writer either.
func TestBundleFingerprintRefusesASymlinkedFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(t.TempDir(), "pipe")
	fifoAt(t, fifo)
	if err := os.Symlink(fifo, filepath.Join(dir, "review.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := withinDeadline(t, fifo, func() error {
		_, err := bundleFingerprint(dir)
		return err
	})
	if !notRegular(err) {
		t.Fatalf("bundleFingerprint = %v, want a not-a-regular-file refusal", err)
	}
}

// TestHashBundleFileFollowsASymlinkToARegularFile pins the symlink decision:
// the fingerprint hashes a linked file exactly as the other bundle readers
// read it, so a link and the same bytes in place fingerprint identically.
func TestHashBundleFileFollowsASymlinkToARegularFile(t *testing.T) {
	t.Parallel()
	linkedDir, plainDir := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "real")
	for path, content := range map[string]string{
		target: "{}\n", filepath.Join(plainDir, "review.json"): "{}\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := os.Symlink(target, filepath.Join(linkedDir, "review.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	linked, plain := sha256.New(), sha256.New()
	if err := hashBundleFile(linked, linkedDir, "review.json"); err != nil {
		t.Fatalf("hash link: %v", err)
	}
	if err := hashBundleFile(plain, plainDir, "review.json"); err != nil {
		t.Fatalf("hash plain: %v", err)
	}
	if !bytes.Equal(linked.Sum(nil), plain.Sum(nil)) {
		t.Error("a symlinked review.json fingerprinted differently from the same bytes in place")
	}
}

// releaseFIFOAtCleanup opens path for writing when the test ends, which lets
// any reader a regressed blocking open left waiting on it return, so a failed
// test does not leave its server goroutine parked on the FIFO.
func releaseFIFOAtCleanup(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
	})
}

// TestPostPreviewRefusesAFIFOInputWithoutHoldingTheLock pins the preview job
// end to end: a review.json that is a FIFO fails the job with the not-regular
// refusal instead of parking it on the open while it holds the bundle lock.
func TestPostPreviewRefusesAFIFOInputWithoutHoldingTheLock(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	fifo := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "review.json")
	fifoAt(t, fifo)
	preview, submit := okPostRunners()
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	// Registered after the server's own cleanups, so it runs before the job
	// manager's stop waits on a job a regression parked on the FIFO.
	releaseFIFOAtCleanup(t, fifo)
	rootID := rootIDOf(t, ts, bearer())
	snap := awaitJobTerminal(t, ts, createPostPreviewJobOn(t, ts, server, rootID, "1234"))
	if asString(t, snap["status"]) != jobStatusFailed || !strings.Contains(string(mustJSON(t, snap)), readcommon.ErrNotRegular.Error()) {
		t.Fatalf("preview over a FIFO review.json = %v, want a failed job naming the not-regular refusal", snap)
	}
}

// TestPostSubmitRefusesAFIFOSwappedInAfterThePreview pins the submit end to
// end: a bundle input swapped for a FIFO after the preview is refused while the
// handler holds the bundle lock, without waiting for a writer, so the submit
// answers and the lock is released for the next write.
func TestPostSubmitRefusesAFIFOSwappedInAfterThePreview(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	review := filepath.Join(mustAbs(t, root), ".verdict", "state", "1234", "review.json")
	overwrite(t, review, `{"body":"as previewed"}`)
	preview, submit := okPostRunners()
	ts, server := postServer(t, &Config{Layer: testLayer(root, nil)}, preview, submit)
	rootID := rootIDOf(t, ts, bearer())
	digest, nonce := mintPreviewSnapshotPair(t, ts, server, rootID)
	if err := os.Remove(review); err != nil {
		t.Fatal(err)
	}
	fifoAt(t, review)
	releaseFIFOAtCleanup(t, review)
	var got result
	err := withinDeadline(t, review, func() error {
		var callErr error
		got, callErr = submitOnce(t.Context(), ts, server, submitBody(rootID, "1234", digest, nonce, new(true)))
		return callErr
	})
	if err != nil {
		t.Fatal(err)
	}
	errObj := asMap(t, decode(t, got.body)["error"])
	if got.status != http.StatusOK || errObj["code"] != codeBundleUnreadable ||
		!strings.Contains(asString(t, errObj["message"]), readcommon.ErrNotRegular.Error()) {
		t.Fatalf("submit over a FIFO review.json = %d %s, want the typed bundle-unreadable refusal the preview answers", got.status, got.body)
	}
	if _, ok := server.previews.peek(nonce, rootID, "1234", digest); !ok {
		t.Error("a refusal that posted nothing and found nothing stale spent the nonce")
	}
	unlock, lockErr := storelock.Acquire(filepath.Join(filepath.Dir(review), "findings.json"), storelock.DefaultWait)
	if lockErr != nil {
		t.Fatalf("the refused submit left the bundle lock held: %v", lockErr)
	}
	unlock()
}
