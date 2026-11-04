// Command tsgen writes (or checks) the TypeScript client's Go-derived
// sources: clients/ts/src/refusal-codes.gen.ts is the refusal-code registry in
// internal/core rendered as a union, so a code the binary can refuse cannot be
// missing from the client's types. Run it through `make clients-ts` (check) or
// `make tsgen` (write); never edit the generated file by hand.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/schemagen"
)

const cliPrefix = "tsgen:"

const codesRelPath = "clients/ts/src/refusal-codes.gen.ts"

type options struct {
	root  string
	write bool
}

// cliError is the command's sealed error type; it never leaks fmt/errors
// values across the run boundary. The cause is the first field so the GC scans
// the fewest pointer bytes; keep that order.
type cliError struct {
	cause error
	msg   string
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
	if err := execute(opts, stdout); err != nil {
		fail(stderr, err)
		return 1
	}
	return 0
}

func fail(stderr io.Writer, err error) {
	fmt.Fprintln(stderr, cliPrefix, err)
}

func execute(opts options, stdout io.Writer) error {
	codes := core.RefusalCodes()
	if len(codes) == 0 {
		return errorf("the refusal-code registry carries no codes")
	}
	path := filepath.Join(opts.root, filepath.FromSlash(codesRelPath))
	data := renderRefusalCodes(codes)
	if opts.write {
		if err := atomic.WriteFile(path, data); err != nil {
			return wrapError("write "+path, err)
		}
		fmt.Fprintf(stdout, "%s wrote %s\n", cliPrefix, codesRelPath)
		return nil
	}
	have, err := os.ReadFile(path)
	if err != nil {
		return wrapError("read "+path, err)
	}
	if bytes.Equal(have, data) {
		fmt.Fprintf(stdout, "%s %s is current\n", cliPrefix, codesRelPath)
		return nil
	}
	return errorf("%s is stale; run `make tsgen` from the repo root", codesRelPath)
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("tsgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "rewrite the generated client sources instead of checking them")
	root := fs.String("root", "", "repository root (default: discovered from the working directory)")
	if err := fs.Parse(args); err != nil {
		return options{}, wrapError("parse flags", err)
	}
	if fs.NArg() != 0 {
		return options{}, errorf("usage: tsgen [-write] [-root dir]")
	}
	if *root != "" {
		return options{root: *root, write: *write}, nil
	}
	found, err := schemagen.RepoRoot()
	if err != nil {
		return options{}, wrapError("locate repository root", err)
	}
	return options{root: found, write: *write}, nil
}

func renderRefusalCodes(codes []string) []byte {
	var out strings.Builder
	out.WriteString("// GENERATED from the refusal-code registry (internal/core/refusalcodes.go)" +
		" by cmd/tsgen. Do not edit.\n")
	out.WriteString("// Open families (missing-*, bad-*) accept any suffix; keep this union\n")
	out.WriteString("// exact and widen at the use site with ` | (string & {})`.\n")
	out.WriteString("export type RefusalCode =\n")
	for _, code := range codes {
		out.WriteString("  | " + strconv.Quote(code) + "\n")
	}
	return []byte(strings.TrimSuffix(out.String(), "\n") + ";\n")
}
