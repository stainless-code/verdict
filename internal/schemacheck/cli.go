package schemacheck

import (
	"fmt"
	"io"
	"strings"
)

// Command is the hidden argv[0] the schema checker runs as. Like the upgrade
// helper it never appears in usage text: the shipped `schemas/validate.sh` is
// the supported surface, and it delegates here.
const Command = "__validate-schema"

// Exit codes the shim passes through unchanged: the shell checker's contract.
const (
	exitInvalid = 1
	exitUsage   = 2
)

// Run validates <schema.json[#/pointer]> <doc.json> and returns the process
// exit code: 0 valid, 1 invalid or unreadable input, 2 bad usage. Every
// rejection is one `FAIL <path>: <message>` line on stderr.
func Run(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintf(stderr, "usage: verdict %s <schema.json[#/pointer]> <doc.json>\n", Command)
		return exitUsage
	}
	path, fragment := splitSpec(args[0])
	checker, err := Load(path, fragment)
	if err != nil {
		fmt.Fprintf(stderr, "FAIL: %v\n", err)
		return exitInvalid
	}
	value, err := Document(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "FAIL: %v\n", err)
		return exitInvalid
	}
	failures := checker.Check(value)
	for _, failure := range failures {
		fmt.Fprintf(stderr, "FAIL %s\n", failure.String())
	}
	if len(failures) > 0 {
		return exitInvalid
	}
	return 0
}

// splitSpec separates "schema.json#/pointer" into its file and fragment; the
// fragment is empty when the whole document is the validation root.
func splitSpec(spec string) (string, string) {
	file, fragment, found := strings.Cut(spec, "#")
	if !found {
		return spec, ""
	}
	return file, "#" + fragment
}
