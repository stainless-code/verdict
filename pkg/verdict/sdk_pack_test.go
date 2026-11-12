package verdict_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	verdict "github.com/stainless-code/verdict/pkg/verdict"
)

// TestClientPackList proves the positionals-free pack read through the SDK:
// the embedded built-in plus a repo pack, and the ListPacks convenience
// returning the same profile.
//
//nolint:revive // the fixture/wire/convenience sequence reads as one scenario
func TestClientPackList(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	pack := filepath.Join(".verdict", "packs", "repo")
	if err := os.MkdirAll(filepath.Join(pack, "lenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSDKFile(t, filepath.Join(pack, "pack.json"),
		`{"schema_version":"1","name":"acme/repo","version":"0.1.0","lenses":[{"id":"style"}]}`)
	writeSDKFile(t, filepath.Join(pack, "lenses", "style.md"), "style body\n")

	c := newSDKClient(nil, nil)
	defer c.Close()
	resp, err := c.Pack(context.Background(), &verdict.PackRequest{List: true})
	requireNoError(t, err)
	if resp.At != sdkAt {
		t.Errorf("at = %q, want %q", resp.At, sdkAt)
	}
	packs, ok := decodeData(t, resp.Data)["packs"].([]any)
	if !ok || len(packs) != 2 {
		t.Fatalf("packs = %v, want builtin + repo", decodeData(t, resp.Data)["packs"])
	}
	repo := asMap(t, packs[1], "repo row")
	if repo["name"] != "acme/repo" || repo["layer"] != "repo" {
		t.Errorf("repo row = %v", repo)
	}
	convenience, err := verdict.ListPacks(context.Background(), c)
	requireNoError(t, err)
	if string(convenience.Data) != string(resp.Data) {
		t.Errorf("ListPacks data = %s, want %s", convenience.Data, resp.Data)
	}
	if _, err := c.Pack(context.Background(), &verdict.PackRequest{}); err == nil {
		t.Fatal("pack without list was accepted")
	}
}

// writeSDKFile writes one fixture file, creating parents.
func writeSDKFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
