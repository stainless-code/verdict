package upgrade

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// HelperCommand is the hidden argv[0] the new binary runs as the Windows
// replacement helper. It never appears in usage text.
const HelperCommand = helperCommand

// handoffWaitTimeout bounds the parent-PID wait; the parent exits right
// after spawning the helper, so a timeout means something is very wrong
// and the helper touches nothing.
const handoffWaitTimeout = 60 * time.Second

// Handoff is the replacement plan the helper executes after the running
// parent exits. Wait and Smoke are injected so every step is unit-testable
// on every OS; the real binary uses waitProcess and defaultSmoke.
type Handoff struct {
	Wait        func(context.Context, int) error
	Smoke       func(context.Context, string, string) error
	Target      string
	New         string
	ReceiptPath string
	ReceiptBody string
	Version     string
	ResultPath  string
	ParentPID   int
}

// Run waits for the parent, renames old→.old, moves the new binary in,
// smoke-tests it, writes the receipt, and deletes .old. Any failure after
// the move restores the old binary and leaves the new image on disk as
// <target>.failed (a running image cannot delete itself on Windows).
func (h *Handoff) Run(ctx context.Context) error {
	wait := h.Wait
	if wait == nil {
		wait = waitProcess
	}
	if err := wait(ctx, h.ParentPID); err != nil {
		return h.finish(fmt.Errorf("waiting for the parent process: %w", err))
	}
	old := h.Target + ".old"
	_ = os.Remove(old) // stale leftover from an interrupted run
	if err := atomic.Replace(h.Target, old); err != nil {
		return h.finish(fmt.Errorf("move the old binary aside: %w", err))
	}
	if err := atomic.Replace(h.New, h.Target); err != nil {
		_ = atomic.Replace(old, h.Target) //nolint:errcheck // best effort: put the old binary back
		return h.finish(fmt.Errorf("move the new binary in: %w", err))
	}
	smoke := h.Smoke
	if smoke == nil {
		smoke = defaultSmoke
	}
	if err := smoke(ctx, h.Target, h.Version); err != nil {
		return h.finish(h.restore(old, fmt.Errorf("the new binary failed its smoke test: %w", err)))
	}
	if err := atomic.WriteFile(h.ReceiptPath, []byte(h.ReceiptBody)); err != nil {
		return h.finish(h.restore(old, fmt.Errorf("write the installer receipt: %w", err)))
	}
	_ = os.Remove(old) // the parent exited, so the old image is deletable
	return h.finish(nil)
}

// restore puts the old binary back and parks the new image as .failed.
func (h *Handoff) restore(old string, cause error) error {
	failed := h.Target + ".failed"
	_ = os.Remove(failed) // stale leftover; the rename below owns the outcome
	if err := atomic.Replace(h.Target, failed); err != nil {
		return newErrf("restoring the old binary failed too: %s; %s", err.Error(), cause.Error())
	}
	if err := atomic.Replace(old, h.Target); err != nil {
		return newErrf("restoring the old binary failed: %s; %s", err.Error(), cause.Error())
	}
	return cause
}

// finish records the helper outcome for the operator and returns err.
func (h *Handoff) finish(err error) error {
	if h.ResultPath == "" {
		return err
	}
	doc := struct {
		Version string `json:"version"`
		Error   string `json:"error,omitempty"`
		OK      bool   `json:"ok"`
	}{OK: err == nil, Version: h.Version}
	if err != nil {
		doc.Error = err.Error()
	}
	raw, marshalErr := json.Marshal(doc)
	if marshalErr != nil {
		return err
	}
	_ = os.WriteFile(h.ResultPath, append(raw, '\n'), filePermResult) //nolint:errcheck // informational result file
	return err
}

// argv renders the helper flags.
func (h *Handoff) argv() []string {
	return []string{
		"--parent", strconv.Itoa(h.ParentPID),
		"--target", h.Target,
		"--new", h.New,
		"--receipt", h.ReceiptPath,
		"--receipt-body", h.ReceiptBody,
		"--version", h.Version,
		"--result", h.ResultPath,
	}
}

// parseHandoff parses the hidden helper flags.
func parseHandoff(args []string) (Handoff, error) {
	fs := flag.NewFlagSet(helperCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	parent := fs.Int("parent", 0, "")
	target := fs.String("target", "", "")
	newPath := fs.String("new", "", "")
	receipt := fs.String("receipt", "", "")
	body := fs.String("receipt-body", "", "")
	version := fs.String("version", "", "")
	result := fs.String("result", "", "")
	if err := fs.Parse(args); err != nil {
		return Handoff{}, jq.Sealed("parse helper flags", err)
	}
	h := Handoff{
		ParentPID: *parent, Target: *target, New: *newPath,
		ReceiptPath: *receipt, ReceiptBody: *body, Version: *version, ResultPath: *result,
	}
	if h.ParentPID <= 0 || h.Target == "" || h.New == "" || h.ReceiptPath == "" || h.Version == "" {
		return Handoff{}, newErr("missing required helper flag")
	}
	return h, nil
}

// RunHelper runs the hidden helper mode and returns the process exit code.
func RunHelper(ctx context.Context, args []string, stderr io.Writer) int {
	h, err := parseHandoff(args)
	if err != nil {
		fmt.Fprintln(stderr, "upgrade-helper: "+err.Error())
		return 2
	}
	waitCtx, cancel := context.WithTimeout(ctx, handoffWaitTimeout)
	defer cancel()
	if err := h.Run(waitCtx); err != nil {
		fmt.Fprintln(stderr, "upgrade-helper: "+err.Error())
		return 1
	}
	fmt.Fprintf(stderr, "upgrade-helper: verdict %s installed\n", h.Version)
	return 0
}

// spawnHandoff starts the new binary as the detached replacement helper.
// The parent must not wait: the helper's first act is waiting for the
// parent PID to disappear.
func spawnHandoff(ctx context.Context, req *Request, helper string, h *Handoff) (string, error) {
	logPath := h.ResultPath + ".log"
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", jq.Sealed("create helper log", err)
	}
	argv := append([]string{helper, helperCommand}, h.argv()...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: staged new binary plus fixed helper flags
	cmd.Env = req.EnvSlice
	cmd.WaitDelay = commandWaitDelay
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close() // start error rules
		return "", jq.Sealed("start helper", err)
	}
	_ = logFile.Close()       // the child holds its own handles
	_ = cmd.Process.Release() //nolint:errcheck // detach: the helper outlives this process
	return logPath, nil
}
