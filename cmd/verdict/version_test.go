package main

import (
	"errors"
	"testing"
)

// TestVersionOfDegrade pins the version-degrade path (F16): with no
// package.json above the binary, --version degrades to 0.0.0-unknown
// instead of failing. The eval suite grades the bash degrade leg only, so
// this test owns the Go side.
func TestVersionOfDegrade(t *testing.T) {
	t.Parallel()
	missing := func(string) ([]byte, error) {
		return nil, errors.New("no package.json above the binary")
	}
	if got := versionOf("/relocated/bin", missing); got != "0.0.0-unknown" {
		t.Errorf("versionOf(missing) = %q; want 0.0.0-unknown", got)
	}
	broken := func(string) ([]byte, error) {
		return []byte(`{broken`), nil
	}
	if got := versionOf("/relocated/bin", broken); got != "0.0.0-unknown" {
		t.Errorf("versionOf(broken) = %q; want 0.0.0-unknown", got)
	}
	empty := func(string) ([]byte, error) {
		return []byte(`{"name":"verdict"}`), nil
	}
	if got := versionOf("/relocated/bin", empty); got != "0.0.0-unknown" {
		t.Errorf("versionOf(no version) = %q; want 0.0.0-unknown", got)
	}
	healthy := func(string) ([]byte, error) {
		return []byte(`{"name":"verdict","version":"0.3.1"}`), nil
	}
	if got := versionOf("/checkout", healthy); got != "0.3.1" {
		t.Errorf("versionOf(healthy) = %q; want 0.3.1", got)
	}
}
