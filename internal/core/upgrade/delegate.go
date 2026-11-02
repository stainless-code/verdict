package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// delegate prints the exact manager command, runs it without a prompt, and
// streams its output. A manager failure propagates its exit code through
// Response.ExitCode so the edge can exit with it.
func delegate(ctx context.Context, req *Request, det *detection) (Response, *core.RefusalError) {
	if det.Channel == ChannelBrew && req.PinVersion != "" {
		return Response{}, &core.RefusalError{
			Code:    codeVersionPin,
			Message: "the Homebrew formula owns version selection; run: brew upgrade " + brewTarget,
		}
	}
	argv := managerUpdate(det.Channel, req.PinVersion)
	if len(argv) == 0 {
		return Response{}, &core.RefusalError{Code: codeUnidentified, Message: "no package-manager update for channel " + det.Channel}
	}
	look := req.LookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look(argv[0])
	if err != nil {
		return Response{}, &core.RefusalError{
			Code:    codeManagerMissing,
			Message: fmt.Sprintf("%s is not on PATH; install it, then re-run: %s", argv[0], strings.Join(argv, " ")),
		}
	}
	req.human("upgrade: running " + strings.Join(argv, " "))
	runArgv := append([]string{path}, argv[1:]...)
	code, err := req.run(ctx, runArgv)
	resp := Response{Channel: det.Channel, Action: ActionDelegate, Command: argv, ExitCode: code}
	if err != nil {
		return resp, &core.RefusalError{Code: codeManagerFailed, Message: fmt.Sprintf("%s failed: %v", argv[0], err)}
	}
	if code != 0 {
		return resp, &core.RefusalError{
			Code:    codeManagerFailed,
			Message: fmt.Sprintf("%s exited %d; the manager owns the install", argv[0], code),
		}
	}
	return resp, nil
}

// run executes one manager argv with the injected streams and environment.
func (req *Request) run(ctx context.Context, argv []string) (int, error) {
	if req.Exec != nil {
		return req.Exec(ctx, argv, req.EnvSlice, req.Stdout, req.Stderr)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: argv comes from the fixed channel table, never agent input
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	cmd.Env = req.EnvSlice
	cmd.WaitDelay = commandWaitDelay
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	return 0, sealed("run "+argv[0], err)
}
