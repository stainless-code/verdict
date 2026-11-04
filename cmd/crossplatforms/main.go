// Command crossplatforms prints one `GOOS/GOARCH` per line for every archive
// row in the release contract, in contract order.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/stainless-code/verdict/release"
)

const cliPrefix = "crossplatforms:"

func main() {
	//nolint:forbidigo // main is the only os.Exit caller
	os.Exit(run(os.Stdout, os.Stderr))
}

func run(stdout, stderr io.Writer) int {
	platforms, err := release.Platforms()
	if err != nil {
		fmt.Fprintln(stderr, cliPrefix, err)
		return 1
	}
	if !printPlatforms(stdout, stderr, platforms) {
		return 1
	}
	return 0
}

func printPlatforms(stdout, stderr io.Writer, platforms []release.Platform) bool {
	if len(platforms) == 0 {
		fmt.Fprintln(stderr, cliPrefix, "the release contract names no archives")
		return false
	}
	for _, platform := range platforms {
		fmt.Fprintf(stdout, "%s/%s\n", platform.GOOS, platform.GOARCH)
	}
	return true
}
