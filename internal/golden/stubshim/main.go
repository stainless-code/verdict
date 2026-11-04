// Command stubshim launches the extensionless bash stub beside it. The
// golden harness copies it to <name>.exe on Windows, where [exec.LookPath]
// resolves only PATHEXT names: the shim forwards its argv to bash verbatim (a
// .cmd shim would re-parse quotes and pipes through cmd.exe) and exits with
// the script's status.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// exitNotFound is bash's status for a missing script or interpreter.
const exitNotFound = 127

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run())
}

func run() int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stubshim:", err)
		return exitNotFound
	}
	script := strings.TrimSuffix(self, filepath.Ext(self))
	//nolint:gosec // G204: the script is the shim's own sibling file; argv is the test's.
	cmd := exec.CommandContext(context.Background(), "bash", append([]string{script}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "stubshim:", err)
		return exitNotFound
	}
	return 0
}
