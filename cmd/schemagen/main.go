// Command schemagen writes (or checks) the published schemas from the Go
// shape types in internal/schemagen. Run it through `make schemas` or
// `go generate ./...`; never edit skills/verdict/schemas/*.schema.json by hand.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/schemagen"
)

const storeDirMode = 0o755

const cliPrefix = "schemagen:"

type options struct { //nolint:govet // option order matches the documented flags
	write bool
	dir   string
}

type cliError struct { //nolint:govet // cause-first reads better than padding for size
	msg   string
	cause error
}

func (e *cliError) Error() string { return e.msg }

func (e *cliError) Unwrap() error { return e.cause }

func errorf(format string, args ...any) error {
	if len(args) == 0 {
		return &cliError{msg: format}
	}
	return &cliError{msg: fmt.Sprintf(format, args...)}
}

func wrapError(prefix string, cause error) error {
	return &cliError{msg: prefix + ": " + cause.Error(), cause: cause}
}

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fail(stderr, err)
		return 2
	}
	if err := execute(opts, stdout, stderr); err != nil {
		fail(stderr, err)
		return 1
	}
	return 0
}

func fail(stderr io.Writer, err error) {
	fmt.Fprintln(stderr, cliPrefix, err)
}

func execute(opts options, stdout, stderr io.Writer) error {
	files, err := schemagen.Generate()
	if err != nil {
		return wrapError("generate", err)
	}
	target, err := resolveDir(opts.dir)
	if err != nil {
		return err
	}
	if opts.write {
		if err := writeAll(target, files); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "%s wrote %d schemas to %s\n", cliPrefix, len(files), target)
		return nil
	}
	return checkAll(target, files, stdout)
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "rewrite skills/verdict/schemas/*.schema.json instead of checking them")
	dir := fs.String("dir", "", "schemas directory (default: <repo>/skills/verdict/schemas)")
	if err := fs.Parse(args); err != nil {
		return options{}, wrapError("parse flags", err)
	}
	if fs.NArg() != 0 {
		return options{}, errorf("usage: schemagen [-write] [-dir schemas]")
	}
	return options{write: *write, dir: *dir}, nil
}

func resolveDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	root, err := schemagen.RepoRoot()
	if err != nil {
		return "", wrapError("locate repository root", err)
	}
	return filepath.Join(root, "skills", "verdict", "schemas"), nil
}

func writeAll(dir string, files []schemagen.File) error {
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		return wrapError("create "+dir, err)
	}
	for _, file := range files {
		if err := atomic.WriteFile(filepath.Join(dir, file.Name), file.Data); err != nil {
			return wrapError("write "+file.Name, err)
		}
	}
	return nil
}

func checkAll(dir string, files []schemagen.File, stdout io.Writer) error {
	stale := false
	for _, file := range files {
		changed, err := checkFile(dir, file, stdout)
		if err != nil {
			return err
		}
		stale = stale || changed
	}
	if stale {
		return errorf("schemas are stale; run `make schemas` from the repo root")
	}
	return nil
}

func checkFile(dir string, file schemagen.File, stdout io.Writer) (bool, error) {
	want, err := os.ReadFile(filepath.Join(dir, file.Name))
	if err != nil {
		return false, wrapError("read "+file.Name, err)
	}
	if bytes.Equal(want, file.Data) {
		return false, nil
	}
	if _, err := fmt.Fprint(stdout, schemagen.DiffFiles(file.Name, want, file.Data)); err != nil {
		return false, wrapError("print diff", err)
	}
	return true, nil
}
