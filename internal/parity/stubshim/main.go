// Command stubshim launches the extensionless bash stub beside it. The parity
// harness copies it to <name>.exe on Windows, where exec.LookPath resolves
// only PATHEXT names: the shim forwards its argv to bash verbatim (a .cmd
// shim would re-parse quotes and pipes through cmd.exe) and exits with the
// script's status.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run())
}

func run() int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stubshim:", err)
		return 127
	}
	script := strings.TrimSuffix(self, filepath.Ext(self))
	//nolint:gosec // G204: the script is the shim's own sibling file; argv is the test's.
	cmd := exec.Command("bash", append([]string{script}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "stubshim:", err)
		return 127
	}
	return 0
}
