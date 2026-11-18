package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core/skill"
)

// TestProcedureSurfacesIgnoreModes proves prompts and resources are byte-for-byte
// the same on every tool surface. The tool counts pin that the three configs
// really are different surfaces.
func TestProcedureSurfacesIgnoreModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		readOnly bool
		operator bool
		tools    int
	}{
		{name: "default", tools: 25},
		{name: "read-only", readOnly: true, tools: 14},
		{name: "operator", operator: true, tools: 26},
	}
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	wantResource, ref := skill.ShowFile(context.Background(), &skill.Request{}, "PROCEDURE.md")
	if ref != nil {
		t.Fatal(ref)
	}
	wantPrompt, err := skillfs.ReviewProcedure()
	if err != nil {
		t.Fatal(err)
	}

	var baseline procedureWire
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.ReadOnly = tc.readOnly
			cfg.Operator = tc.operator
			lines := []string{
				`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`,
				`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"review"}}`,
				`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
				fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":%q}}`,
					resourceURIPrefix+"PROCEDURE.md"),
			}
			frames := serve(t, &cfg, lines...)
			for id := range lines {
				if frame := frames[fmt.Sprint(id+1)]; frame.Error != nil {
					t.Fatalf("request %d error = %+v", id+1, frame.Error)
				}
			}
			var tools toolsListResult
			if err := json.Unmarshal(frames["1"].Result, &tools); err != nil {
				t.Fatal(err)
			}
			if len(tools.Tools) != tc.tools {
				t.Fatalf("%s tools = %d, want %d", tc.name, len(tools.Tools), tc.tools)
			}
			var prompts promptsListResult
			if err := json.Unmarshal(frames["2"].Result, &prompts); err != nil {
				t.Fatal(err)
			}
			if len(prompts.Prompts) != len(skillfs.Tasks()) {
				t.Fatalf("%s prompts = %d, want %d", tc.name, len(prompts.Prompts), len(skillfs.Tasks()))
			}
			var prompt promptResult
			if err := json.Unmarshal(frames["3"].Result, &prompt); err != nil {
				t.Fatal(err)
			}
			if len(prompt.Messages) != 1 || !bytes.Equal([]byte(prompt.Messages[0].Content.Text), wantPrompt) {
				t.Fatalf("%s review prompt does not carry the binary's Procedure", tc.name)
			}
			var resources resourceListResult
			if err := json.Unmarshal(frames["4"].Result, &resources); err != nil {
				t.Fatal(err)
			}
			if len(resources.Resources) != len(paths) {
				t.Fatalf("%s resources = %d, want %d", tc.name, len(resources.Resources), len(paths))
			}
			var content resourceReadResult
			if err := json.Unmarshal(frames["5"].Result, &content); err != nil {
				t.Fatal(err)
			}
			if len(content.Contents) != 1 || !bytes.Equal([]byte(content.Contents[0].Text), wantResource) {
				t.Fatalf("%s PROCEDURE.md resource is not byte-identical to skill show", tc.name)
			}

			wire := procedureWire{
				promptsList:   frames["2"].Result,
				promptGet:     frames["3"].Result,
				resourcesList: frames["4"].Result,
				resourceRead:  frames["5"].Result,
			}
			if i == 0 {
				baseline = wire
				return
			}
			if !bytes.Equal(wire.promptsList, baseline.promptsList) {
				t.Errorf("%s prompts/list differs from the default surface", tc.name)
			}
			if !bytes.Equal(wire.promptGet, baseline.promptGet) {
				t.Errorf("%s prompts/get differs from the default surface", tc.name)
			}
			if !bytes.Equal(wire.resourcesList, baseline.resourcesList) {
				t.Errorf("%s resources/list differs from the default surface", tc.name)
			}
			if !bytes.Equal(wire.resourceRead, baseline.resourceRead) {
				t.Errorf("%s resources/read differs from the default surface", tc.name)
			}
		})
	}
}

type procedureWire struct {
	promptsList   json.RawMessage
	promptGet     json.RawMessage
	resourcesList json.RawMessage
	resourceRead  json.RawMessage
}
