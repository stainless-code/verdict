package verdict_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	verdict "github.com/stainless-code/verdict/pkg/verdict"
)

// TestClientPackList proves the positionals-free pack read through the SDK:
// the thirteen embedded built-in packs (the core pack plus the twelve curated
// language packs) ahead of a repo pack, and the ListPacks convenience
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
	if !ok || len(packs) != 14 {
		t.Fatalf("packs = %v, want the 13 builtin packs and 1 repo pack", decodeData(t, resp.Data)["packs"])
	}
	core := asMap(t, packs[0], "core row")
	if core["name"] != "verdict/builtin" || core["layer"] != "builtin" {
		t.Errorf("core row = %v", core)
	}
	repo := asMap(t, packs[13], "repo row")
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

// TestClientCheckPack proves the SDK resolves one path through the shipped
// layers: the Go lens and its rules for a Go file, the TypeScript lens and
// none of the Go rules for a TypeScript file, and a refusal for a path that
// escapes the repository.
func TestClientCheckPack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	c := newSDKClient(nil, nil)
	defer c.Close()
	goPack, err := verdict.CheckPack(context.Background(), c, "internal/store/store.go")
	requireNoError(t, err)
	data := decodeData(t, goPack.Data)
	if data["path"] != "internal/store/store.go" {
		t.Errorf("path = %v", data["path"])
	}
	if got, want := ids(data["lenses"]), "api-contract,go,perf,security,test"; got != want {
		t.Errorf("go lenses = %q, want %q", got, want)
	}
	if got, want := ids(data["rules"]), "go-context-lifecycle,go-defer-loop,go-error-loss,go-mutex-misuse,"+
		"go-resource-release,go-shared-state,go-slice-aliasing,sec-broken-authz,sec-injection,"+
		"sec-jwt-validation,sec-path-traversal,sec-ssrf,sec-timing-compare,sec-unsafe-deserialization,"+
		"sec-weak-random"; got != want {
		t.Errorf("go rules = %q, want %q", got, want)
	}
	tsPack, err := verdict.CheckPack(context.Background(), c, "src/app.tsx")
	requireNoError(t, err)
	if got, want := ids(decodeData(t, tsPack.Data)["lenses"]), "api-contract,perf,react,security,test,ts-js"; got != want {
		t.Errorf("ts lenses = %q, want %q", got, want)
	}
	_, err = verdict.CheckPack(context.Background(), c, "../outside.go")
	if err == nil {
		t.Fatal("an escaping path was accepted")
	}
	refusal := &verdict.RefusalError{}
	if !errors.As(err, &refusal) || refusal.Code != "bad-path" {
		t.Fatalf("escape refusal = %v, want bad-path", err)
	}
}

// ids joins the id field of one wire row list.
func ids(value any) string {
	rows, ok := value.([]any)
	if !ok {
		return ""
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return strings.Join(out, ",")
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
