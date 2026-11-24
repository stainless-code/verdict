package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core/skill"
)

type procedureModeCase struct {
	name     string
	tools    int
	readOnly bool
	operator bool
}

// TestProcedureSurfacesIgnoreModes proves prompts and resources are
// byte-for-byte the same on every tool surface. The tool counts pin that the
// three configs really are different surfaces.
func TestProcedureSurfacesIgnoreModes(t *testing.T) {
	t.Parallel()
	cases := []procedureModeCase{
		{name: "default", tools: 32},
		{name: "read-only", tools: 21, readOnly: true},
		{name: "operator", tools: 33, operator: true},
	}
	paths, wantPrompt, wantResource := procedureFixtures(t)

	var baseline procedureWire
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.ReadOnly = tc.readOnly
			cfg.Operator = tc.operator
			frames := procedureFrames(t, &cfg)
			checkProcedureSurface(t, tc, frames, len(paths)+len(handoffRefPaths())+4, len(skillfs.Tasks()), wantPrompt, wantResource)
			wire := procedureWireFrom(frames)
			if i == 0 {
				baseline = wire
				return
			}
			checkProcedureWire(t, tc.name, &wire, &baseline)
		})
	}
}

func procedureFixtures(t *testing.T) ([]string, []byte, []byte) {
	t.Helper()
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
	return paths, servedRefPaths(wantPrompt, "review"), wantResource
}

func procedureFrames(t *testing.T, cfg *Config) map[string]frame {
	t.Helper()
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"review"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":%q}}`,
			resourceURIPrefix+"PROCEDURE.md"),
		`{"jsonrpc":"2.0","id":6,"method":"resources/templates/list"}`,
	}
	frames := serve(t, cfg, lines...)
	for i := range lines {
		id := strconv.Itoa(i + 1)
		if frame := frames[id]; frame.Error != nil {
			t.Fatalf("request %s error = %+v", id, frame.Error)
		}
	}
	return frames
}

func checkProcedureSurface(t *testing.T, tc procedureModeCase, frames map[string]frame,
	pathCount, taskCount int, wantPrompt, wantResource []byte,
) {
	t.Helper()
	checkToolCount(t, tc, frames)
	checkPromptSurface(t, tc.name, frames, taskCount, wantPrompt)
	checkResourceSurface(t, tc.name, frames, pathCount, wantResource)
}

func checkToolCount(t *testing.T, tc procedureModeCase, frames map[string]frame) {
	t.Helper()
	var tools toolsListResult
	if err := json.Unmarshal(frames["1"].Result, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != tc.tools {
		t.Fatalf("%s tools = %d, want %d", tc.name, len(tools.Tools), tc.tools)
	}
}

func checkPromptSurface(t *testing.T, name string, frames map[string]frame, taskCount int, want []byte) {
	t.Helper()
	var prompts promptsListResult
	if err := json.Unmarshal(frames["2"].Result, &prompts); err != nil {
		t.Fatal(err)
	}
	if len(prompts.Prompts) != taskCount {
		t.Fatalf("%s prompts = %d, want %d", name, len(prompts.Prompts), taskCount)
	}
	var prompt promptResult
	if err := json.Unmarshal(frames["3"].Result, &prompt); err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 || !bytes.Equal([]byte(prompt.Messages[0].Content.Text), want) {
		t.Fatalf("%s review prompt does not carry the binary's Procedure", name)
	}
}

func checkResourceSurface(t *testing.T, name string, frames map[string]frame, pathCount int, want []byte) {
	t.Helper()
	var resources resourceListResult
	if err := json.Unmarshal(frames["4"].Result, &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) != pathCount {
		t.Fatalf("%s resources = %d, want %d", name, len(resources.Resources), pathCount)
	}
	var content resourceReadResult
	if err := json.Unmarshal(frames["5"].Result, &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Contents) != 1 || !bytes.Equal([]byte(content.Contents[0].Text), want) {
		t.Fatalf("%s PROCEDURE.md resource is not byte-identical to skill show", name)
	}
}

type procedureWire struct {
	promptsList   json.RawMessage
	promptGet     json.RawMessage
	resourcesList json.RawMessage
	resourceRead  json.RawMessage
	templatesList json.RawMessage
}

func procedureWireFrom(frames map[string]frame) procedureWire {
	return procedureWire{
		promptsList:   frames["2"].Result,
		promptGet:     frames["3"].Result,
		resourcesList: frames["4"].Result,
		resourceRead:  frames["5"].Result,
		templatesList: frames["6"].Result,
	}
}

func checkProcedureWire(t *testing.T, name string, got, want *procedureWire) {
	t.Helper()
	if !bytes.Equal(got.promptsList, want.promptsList) {
		t.Errorf("%s prompts/list differs from the default surface", name)
	}
	if !bytes.Equal(got.promptGet, want.promptGet) {
		t.Errorf("%s prompts/get differs from the default surface", name)
	}
	if !bytes.Equal(got.resourcesList, want.resourcesList) {
		t.Errorf("%s resources/list differs from the default surface", name)
	}
	if !bytes.Equal(got.resourceRead, want.resourceRead) {
		t.Errorf("%s resources/read differs from the default surface", name)
	}
	if !bytes.Equal(got.templatesList, want.templatesList) {
		t.Errorf("%s resources/templates/list differs from the default surface", name)
	}
}
