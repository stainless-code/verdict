// Command schemagen writes (or checks) the published schemas from the Go
// shape types in internal/schemagen. Run it through `make schemas` or
// `go generate ./...`; never edit schemas/*.schema.json by hand.
// In check mode it also re-checks the additivity baseline under
// internal/schemagen/testdata/baseline byte for byte.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/schemagen"
)

const storeDirMode = 0o755

// shippedDir and baselineDir are the two committed copies of the generated
// schemas, relative to the repository root, each with the target that rewrites
// it.
const (
	shippedDir   = "schemas"
	baselineDir  = "internal/schemagen/testdata/baseline"
	shippedHint  = "run `make schemas` from the repo root"
	baselineHint = "run `make schemas-baseline` from the repo root"
)

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
	if opts.write {
		target, err := resolveDir(opts.dir)
		if err != nil {
			return err
		}
		if err := writeAll(target, files); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "%s wrote %d schemas to %s\n", cliPrefix, len(files), target)
		return nil
	}
	return checkCommitted(opts.dir, files, stdout)
}

// checkCommitted checks every committed copy of the generated schemas: the
// shipped directory, and — in the default layout — the additivity baseline,
// compared whole. The additivity test only reports what a baseline still
// promises, so a baseline that lags the generator by additions alone passes it;
// the byte comparison is the gate that sees that.
func checkCommitted(dir string, files []schemagen.File, stdout io.Writer) error {
	if dir != "" {
		return checkAll(dir, dir, hintFor(dir), files, stdout)
	}
	root, err := schemagen.RepoRoot()
	if err != nil {
		return wrapError("locate repository root", err)
	}
	if err := checkAll(filepath.Join(root, shippedDir), shippedDir, shippedHint, files, stdout); err != nil {
		return err
	}
	return checkAll(filepath.Join(root, baselineDir), baselineDir, baselineHint, files, stdout)
}

// hintFor names the command that rewrites dir when it was given explicitly: the
// baseline target when dir is the baseline, else the -write form for that
// directory. The shipped target would leave either one stale.
func hintFor(dir string) string {
	if root, err := schemagen.RepoRoot(); err == nil && samePath(dir, filepath.Join(root, baselineDir)) {
		return baselineHint
	}
	return "run `go run ./cmd/schemagen -write -dir " + dir + "`"
}

// samePath reports whether two paths address the same directory.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	write := flags.Bool("write", false, "rewrite schemas/*.schema.json instead of checking them")
	dir := flags.String("dir", "", "schemas directory (default: <repo>/schemas)")
	if err := flags.Parse(args); err != nil {
		return options{}, wrapError("parse flags", err)
	}
	if flags.NArg() != 0 {
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
	return filepath.Join(root, shippedDir), nil
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

// checkAll prints a diff for every file in dir that is not the generated bytes
// and fails naming label (how a reader addresses dir) and the target that
// rewrites it.
func checkAll(dir, label, hint string, files []schemagen.File, stdout io.Writer) error {
	stale := false
	for _, file := range files {
		changed, err := checkFile(dir, file, stdout)
		if err != nil {
			return err
		}
		stale = stale || changed
	}
	if stale {
		return errorf("%s is stale; %s", label, hint)
	}
	return nil
}

func checkFile(dir string, file schemagen.File, stdout io.Writer) (bool, error) {
	path := filepath.Join(dir, file.Name)
	want, err := os.ReadFile(path)
	if errors.Is(err, iofs.ErrNotExist) {
		fmt.Fprintf(stdout, "missing: %s\n", file.Name)
		return true, nil
	}
	if err != nil {
		return false, wrapError("read "+path, err)
	}
	if bytes.Equal(want, file.Data) {
		return false, nil
	}
	if _, err := fmt.Fprint(stdout, schemagen.DiffFiles(file.Name, want, file.Data)); err != nil {
		return false, wrapError("print diff", err)
	}
	return true, nil
}
