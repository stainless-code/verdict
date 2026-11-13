package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestPostFloorDecoder(t *testing.T) {
	a, err := newArgs(json.RawMessage(`{"flags":{"minSeverity":"important"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var req core.PostRequest
	decodePost(&req, a)
	if err := a.done(); err != nil {
		t.Fatal(err)
	}
	if req.Flags.MinSeverity != "important" {
		t.Fatalf("floor = %q; want important", req.Flags.MinSeverity)
	}
}

func TestFetchExclusionOverrideDecoder(t *testing.T) {
	a, err := newArgs(json.RawMessage(`{"flags":{"ignore":["**","!keep.go"],"noDefaultExcludes":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	var req core.FetchRequest
	decodeFetch(&req, a)
	if err := a.done(); err != nil {
		t.Fatal(err)
	}
	if !req.Flags.NoDefaultExcludes || len(req.Flags.Ignore) != 2 || req.Flags.Ignore[1] != "!keep.go" {
		t.Fatalf("flags = %+v", req.Flags)
	}
}

func TestFetchLocalExclusionOverrideDecoder(t *testing.T) {
	a, err := newArgs(json.RawMessage(`{"flags":{"ignore":["a.go","b.go"],"noDefaultExcludes":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	var local core.FetchLocalRequest
	decodeFetchLocal(&local, a)
	if err := a.done(); err != nil {
		t.Fatal(err)
	}
	if !local.Flags.NoDefaultExcludes || len(local.Flags.Ignore) != 2 || local.Flags.Ignore[1] != "b.go" {
		t.Fatalf("flags = %+v", local.Flags)
	}
}
