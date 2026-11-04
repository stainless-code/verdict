package main

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestVersionOfDegrade pins the version-degrade path (F16): with no
// package.json above the binary, --version degrades to the shared
// core.VersionUnknown (0.0.0-unknown) instead of failing. The eval suite
// grades the degrade leg, so this test owns the Go side.
func TestVersionOfDegrade(t *testing.T) {
	t.Parallel()
	missing := func(string) ([]byte, error) {
		return nil, errors.New("no package.json above the binary")
	}
	if got := versionOf("/relocated/bin", missing); got != core.VersionUnknown {
		t.Errorf("versionOf(missing) = %q; want core.VersionUnknown", got)
	}
	broken := func(string) ([]byte, error) {
		return []byte(`{broken`), nil
	}
	if got := versionOf("/relocated/bin", broken); got != core.VersionUnknown {
		t.Errorf("versionOf(broken) = %q; want core.VersionUnknown", got)
	}
	empty := func(string) ([]byte, error) {
		return []byte(`{"name":"verdict"}`), nil
	}
	if got := versionOf("/relocated/bin", empty); got != core.VersionUnknown {
		t.Errorf("versionOf(no version) = %q; want core.VersionUnknown", got)
	}
	healthy := func(string) ([]byte, error) {
		return []byte(`{"name":"verdict","version":"0.3.1"}`), nil
	}
	if got := versionOf("/checkout", healthy); got != "0.3.1" {
		t.Errorf("versionOf(healthy) = %q; want 0.3.1", got)
	}
}
