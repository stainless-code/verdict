package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type contractArchive struct {
	Goos   string `json:"goos"`
	Goarch string `json:"goarch"`
}

func shippedPlatforms(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "release", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Archives []contractArchive `json:"archives"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	pairs := make([]string, 0, len(doc.Archives))
	for _, archive := range doc.Archives {
		pairs = append(pairs, archive.Goos+"/"+archive.Goarch)
	}
	return pairs
}

// TestRunPrintsEveryShippedPlatform compares run's stdout with the contract
// rows.
func TestRunPrintsEveryShippedPlatform(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(&stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stderr = %q", code, stderr.String())
	}
	want := strings.Join(shippedPlatforms(t), "\n") + "\n"
	if stdout.String() != want {
		t.Errorf("run printed\n%s\nwant\n%s", stdout.String(), want)
	}
}

// TestPrintPlatformsRefusesEmptyMatrix fails an empty matrix without output.
func TestPrintPlatformsRefusesEmptyMatrix(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if printPlatforms(&stdout, &stderr, nil) {
		t.Fatal("printPlatforms accepted an empty matrix")
	}
	if stdout.Len() != 0 {
		t.Errorf("printPlatforms wrote %q to stdout for an empty matrix", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "names no archives") {
		t.Errorf("stderr = %q; want the empty-contract refusal", got)
	}
}
