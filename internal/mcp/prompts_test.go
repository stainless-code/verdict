package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// promptCopy is the exact one-line description each task advertises to hosts.
var promptCopy = map[string]string{
	"address-reviews":    "Address review threads with verified fixes and one reply per thread.",
	"fix-ci":             "Diagnose a PR-introduced CI failure, fix it, and push the fix with a summary.",
	"incremental-review": "Review only the commits added since a prior review, with a no-review note for trivial deltas.",
	"review":             "Review a fetched PR or local diff and write one review.json.",
	"validate":           "Validate candidate findings against the bundle without changing it.",
}

type promptResult struct {
	Description string `json:"description"`
	Messages    []struct {
		Role    string `json:"role"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

// TestPromptsListSnapshot pins the advertised names and their user-facing copy.
func TestPromptsListSnapshot(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)
	if frames["1"].Error != nil {
		t.Fatalf("prompts/list error = %+v", frames["1"].Error)
	}
	var result struct {
		Prompts []map[string]any `json:"prompts"`
	}
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Prompts) != len(promptCopy) {
		t.Fatalf("prompts = %d, want %d", len(result.Prompts), len(promptCopy))
	}
	for _, prompt := range result.Prompts {
		if len(prompt) != 2 {
			t.Errorf("prompt fields = %v, want only name and description", prompt)
		}
		name, ok := prompt["name"].(string)
		if !ok {
			t.Fatalf("prompt has no name: %v", prompt)
		}
		want, known := promptCopy[name]
		if !known {
			t.Errorf("prompts/list advertises unknown task %q", name)
			continue
		}
		if prompt["description"] != want {
			t.Errorf("%s description = %v, want %q", name, prompt["description"], want)
		}
	}
}

// TestPromptsGetBodies proves each prompt carries its exact task Procedure.
// The expected bodies come from literal file lists, never from TaskProcedure.
func TestPromptsGetBodies(t *testing.T) {
	t.Parallel()
	expected := map[string][]byte{
		"address-reviews": literalProcedure(t,
			"PROCEDURE.md", "prompts/system.md", "prompts/address-reviews.md"),
		"fix-ci": literalProcedure(t,
			"PROCEDURE.md", "prompts/system.md", "prompts/fix-ci.md"),
		"incremental-review": literalProcedure(t,
			"PROCEDURE.md", "prompts/system.md", "prompts/incremental-review.md"),
		"review": literalProcedure(t,
			"PROCEDURE.md", "prompts/system.md", "prompts/review.md", "prompts/diagrams.md"),
		"validate": literalProcedure(t,
			"PROCEDURE.md", "prompts/system.md", "prompts/validate.md"),
	}
	tasks := skillfs.Tasks()
	lines := make([]string, 0, len(tasks))
	for i, task := range tasks {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"prompts/get","params":{"name":%q}}`, i+1, task))
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for i, task := range tasks {
		frame := frames[strconv.Itoa(i+1)]
		if frame.Error != nil {
			t.Fatalf("prompts/get %s error = %+v", task, frame.Error)
		}
		var result promptResult
		if err := json.Unmarshal(frame.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Description != promptCopy[task] {
			t.Errorf("%s result description = %q, want %q", task, result.Description, promptCopy[task])
		}
		if len(result.Messages) != 1 {
			t.Fatalf("%s messages = %d, want one", task, len(result.Messages))
		}
		message := result.Messages[0]
		if message.Role != "user" || message.Content.Type != contentTypeText {
			t.Errorf("%s message = %+v, want one user text block", task, message)
		}
		if want := expected[task]; !bytes.Equal([]byte(message.Content.Text), want) {
			t.Errorf("%s text differs from the literal %d-byte Procedure", task, len(want))
		}
	}
	assertNamedProcedure(t, frames[strconv.Itoa(indexOf(tasks, "review")+1)].Result, "review", skillfs.ReviewProcedure)
	assertNamedProcedure(t, frames[strconv.Itoa(indexOf(tasks, "address-reviews")+1)].Result, "address-reviews", skillfs.DrainProcedure)
}

// TestPromptUnknownName pins the spec's invalid-params refusal and its list of
// valid task names for both an unknown and a missing name.
func TestPromptUnknownName(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{}}`,
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for _, id := range []string{"1", "2"} {
		got := frames[id].Error
		if got == nil || got.Code != codeInvalidParams {
			t.Fatalf("prompts/get id %s = %+v, want -32602", id, got)
		}
		for _, task := range skillfs.Tasks() {
			if !strings.Contains(got.Message, task) {
				t.Errorf("prompts/get id %s message %q omits %q", id, got.Message, task)
			}
		}
	}
	if !strings.Contains(frames["1"].Error.Message, "nope") {
		t.Errorf("unknown prompt message = %q, want the bad name", frames["1"].Error.Message)
	}
}

func literalProcedure(t *testing.T, files ...string) []byte {
	t.Helper()
	var want []byte
	for _, name := range files {
		part, err := skillfs.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, part...)
	}
	return want
}

func indexOf(names []string, want string) int {
	for i, name := range names {
		if name == want {
			return i
		}
	}
	return -1
}

func assertNamedProcedure(t *testing.T, raw json.RawMessage, task string, named func() ([]byte, error)) {
	t.Helper()
	var result promptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	want, err := named()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || !bytes.Equal([]byte(result.Messages[0].Content.Text), want) {
		t.Errorf("%s text differs from its named Procedure", task)
	}
}
