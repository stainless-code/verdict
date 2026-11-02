package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// outcome carries the watchdog result for the patch record.
type outcome struct {
	tail     string
	exit     int
	timedOut bool
}

// outFilePerm allows the harness and operators to read the raw verify
// output the command wrote.
const outFilePerm = 0o666

// orphanMarker records a decoy exit: the parent finished while its tree
// outlived it, so the proof is refused.
const orphanMarker = "orphaned command tree killed after exit\n"

// execute runs `bash -c vcmd` under the timeout watchdog, mirroring the
// shell: truncate .verify.out/.verify.timeout first, spawn in its own
// process group, poll for completion, SIGKILL the group on timeout, then
// sweep for orphans that outlived the parent. The exit plus a redacted tail
// come back for the patch record; I/O failures come back as errors for the
// caller to refuse as verify-failed.
//
// Timeout visibility comes from ctx.Err() (GO-EXEC-05): a deadline breach
// kills the group and records the timeout, never a hang.
func execute(ctx context.Context, dir, vcmd string, timeoutSecs int, warn func(string), env []string) (outcome, error) {
	outPath := dir + "/.verify.out"
	timeoutPath := dir + "/.verify.timeout"
	if err := truncateOutputs(outPath, timeoutPath); err != nil {
		return outcome{}, err
	}

	timeout := time.Duration(timeoutSecs) * time.Second
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, waitDone, outFile, err := spawnWatched(tctx, vcmd, outPath, env)
	if err != nil {
		return outcome{}, err
	}
	defer outFile.Close()
	timedOut := runWatchdog(tctx, cmd, timeoutSecs, warn)
	exit := waitExit(cmd, waitDone, timedOut)
	if timedOut, err = recordTimeoutMarker(timeoutPath, timeoutSecs, timedOut, cmd); err != nil {
		return outcome{}, err
	}
	if ctxErr := tctx.Err(); ctxErr == context.DeadlineExceeded {
		timedOut = true
	}
	tail := lastBytes(outPath, tailCap)
	// Command substitution strips trailing newlines from the shell's
	// tail; trim the same way before redaction.
	tail = strings.TrimRight(tail, "\n")
	tail = redactSecrets(tail)
	return outcome{exit: exit, timedOut: timedOut, tail: tail}, nil
}

// truncateOutputs empties the watchdog outputs before a run, mirroring
// the shell's `: >` truncations.
func truncateOutputs(paths ...string) error {
	for _, p := range paths {
		if err := os.WriteFile(p, nil, outFilePerm); err != nil {
			return err
		}
	}
	return nil
}

// spawnWatched starts `bash -c vcmd` in its own process group with output
// captured to outPath, returning the command, its wait channel, and the
// open output file (closed by the caller).
func spawnWatched(tctx context.Context, vcmd, outPath string, env []string) (*exec.Cmd, chan error, *os.File, error) {
	cmd := exec.CommandContext(tctx, "bash", "-c", vcmd)
	// Inherit the binary's cwd (the bundle root): the shell spawns
	// `bash -c` the same way, so relative verifyCmds resolve identically.
	setGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = waitDelay
	outFile, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, outFilePerm)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, nil, nil, fmt.Errorf("open %s: %v", outPath, err)
	}
	cmd.Stdout = outFile
	cmd.Stderr = outFile
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		_ = outFile.Close()
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, nil, nil, fmt.Errorf("start verify command: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	return cmd, waitDone, outFile, nil
}

// runWatchdog polls for completion (or warns without a timeout when ps
// is unavailable) and reports whether the budget expired.
func runWatchdog(tctx context.Context, cmd *exec.Cmd, timeoutSecs int, warn func(string)) bool {
	if havePS() {
		return pollTimeout(tctx, cmd, timeoutSecs)
	}
	return waitNoPS(warn)
}

// recordTimeoutMarker writes the timeout marker for a timed-out run, or
// runs the orphan sweep for a clean exit that may have left its tree
// behind, reporting the final timedOut state.
func recordTimeoutMarker(timeoutPath string, timeoutSecs int, timedOut bool, cmd *exec.Cmd) (bool, error) {
	if !timedOut {
		if sweepOrphans(cmd) {
			if err := os.WriteFile(timeoutPath, []byte(orphanMarker), outFilePerm); err != nil {
				return true, err
			}
			return true, nil
		}
		return false, nil
	}
	if err := os.WriteFile(timeoutPath, []byte(timeoutLine(timeoutSecs)), outFilePerm); err != nil {
		return true, err
	}
	return true, nil
}

// pollTimeout ports the polling watchdog: while the main pid reads alive
// (kill -0 plus the ps zombie arm) and the budget holds, sleep one second;
// on expiry SIGKILL the group and record the timeout. The wait result is
// never consumed here: the channel is buffered so the waiter never blocks,
// and waitExit takes the single receive (reaping before the orphan sweep).
func pollTimeout(tctx context.Context, cmd *exec.Cmd, timeoutSecs int) bool {
	elapsed := 0
	for {
		if !processAlive(tctx, cmd) {
			return false
		}
		if elapsed >= timeoutSecs {
			break
		}
		if waitPollSecond(tctx) {
			break
		}
		elapsed++
	}
	return stopTimedOut(cmd)
}

// stopTimedOut SIGKILLs the expired group. The kill is best effort: the
// process may have exited between the last liveness check and the kill,
// in which case there is nothing left to stop. Either way the timeout
// stands.
func stopTimedOut(cmd *exec.Cmd) bool {
	if err := killGroup(cmd); err != nil {
		return true
	}
	return true
}

// waitPollSecond sleeps one watchdog tick, reporting whether the
// deadline context fired during the sleep.
func waitPollSecond(tctx context.Context) bool {
	select {
	case <-tctx.Done():
		return true
	case <-time.After(time.Second):
	}
	return tctx.Err() != nil
}

// waitNoPS ports the no-ps fallback: warn loudly and wait without a
// timeout, exactly like the shell's unenforced path. The wait result stays
// for waitExit's single receive.
func waitNoPS(warn func(string)) bool {
	warn("no ps(1) — timeout unenforced for this run")
	return false
}

// waitExit drains the wait result and maps it to the shell `wait` code:
// 128+signo for signaled processes, the exit status otherwise. A
// timeout-killed tree reads 137 (SIGKILL), matching the shell.
func waitExit(_ *exec.Cmd, waitDone chan error, timedOut bool) int {
	err := <-waitDone
	if err == nil {
		return 0
	}
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if ok {
		return waitStatusCode(exitErr, timedOut)
	}
	if timedOut {
		return sigkillExit
	}
	return 1
}

// waitStatusCode maps a decoded wait status to the shell `wait` code:
// 128+signo for signaled processes, the exit status otherwise. An
// undecodable status reads 1, or 137 on a timed-out tree.
func waitStatusCode(exitErr *exec.ExitError, timedOut bool) int {
	ws, ok := waitStatusOf(exitErr)
	if !ok {
		if timedOut {
			return sigkillExit
		}
		return 1
	}
	if ws.signaled {
		return sigOffset + ws.signal
	}
	return ws.status
}

// timeoutLine renders the timeout marker for .verify.timeout.
func timeoutLine(timeoutSecs int) string {
	return "timeout after " + itoa(timeoutSecs) + "s\n"
}

// decimalBase is the radix for small-int rendering.
const decimalBase = 10

// itoa renders a small non-negative int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%decimalBase)
		n /= decimalBase
	}
	return string(b[i:])
}

// lastBytes reads the last n bytes of a file (the `tail -c` shape).
func lastBytes(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	return string(raw)
}

// redactSecrets ports the known-secret redaction of the recorded tail:
// every environment variable whose name matches the secret suffix list
// (case-insensitive, trailing rotation digits/underscores stripped) has
// its value — when 8+ chars — replaced literally. The full .verify.out
// keeps raw output for operator debugging; only the tail is filtered.
func redactSecrets(tail string) string {
	for _, name := range secretNames() {
		val := os.Getenv(name)
		if len(val) < secretMinLen {
			continue
		}
		tail = strings.ReplaceAll(tail, val, redacted)
	}
	return tail
}

// secretNames lists environment names matching the secret suffix list in
// compgen (alphabetical) order.
func secretNames() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if isSecretName(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// stripRotationSuffix removes trailing rotation digits and
// underscores before the suffix match, like the shell loop.
func stripRotationSuffix(name string) string {
	norm := name
	for len(norm) > 0 {
		last := norm[len(norm)-1]
		if (last < '0' || last > '9') && last != '_' {
			break
		}
		norm = norm[:len(norm)-1]
	}
	return norm
}

// isSecretName ports the nocasematch suffix match with trailing
// rotation-digit/underscore stripping.
func isSecretName(name string) bool {
	lower := strings.ToLower(stripRotationSuffix(name))
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// Redaction constants, mirroring the shell loop.
var secretSuffixes = []string{
	"_token", "_secret", "_key", "_password", "_passphrase", "_pat",
	"_auth", "_credential", "_credentials", "_pass", "_pwd",
	"token", "secret", "key", "password",
	"aws_access_key_id", "ssh_auth_sock", "ssh_agent_pid",
}

// secretMinLen skips empty and short (<8 char) values, like the shell.
const secretMinLen = 8

// redacted is the tail replacement marker.
const redacted = "***REDACTED***"

// tailCap is the recorded tail size (tail -c 4000).
const tailCap = 4000

// sigkillExit is the shell `wait` code for a SIGKILLed command.
const sigkillExit = 137

// sigOffset maps a fatal signal to the shell `wait` code (128+signo).
const sigOffset = 128
