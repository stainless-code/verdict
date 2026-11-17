package run

import (
	"slices"
	"testing"
)

// TestArgvForPromptPlacement pins the one shape change in STA-440: a row that
// takes its prompt as a flag value puts that flag immediately before the
// directive, after every operator argument, so a pass-through flag cannot be
// swallowed as its value. Rows without PromptFlag keep the positional shape.
func TestArgvForPromptPlacement(t *testing.T) {
	tests := []struct {
		name string
		def  agentDef
		want []string
	}{
		{
			name: "positional prompt",
			def: agentDef{
				Name: "claude", Binary: "claude", Args: []string{"-p"}, ModelFlag: modelFlag,
			},
			want: []string{"claude", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "flag value prompt",
			def: agentDef{
				Name: "amp", Binary: "amp", Args: nil, PromptFlag: "-x", ModelFlag: modelFlag,
			},
			want: []string{"amp", "--model", "M", "--x", "-x", "PROMPT"},
		},
		{
			name: "flag value prompt after a subcommand",
			def: agentDef{
				Name: "goose", Binary: "goose", Args: []string{"run"}, PromptFlag: "-t", ModelFlag: modelFlag,
			},
			want: []string{"goose", "run", "--model", "M", "--x", "-t", "PROMPT"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := tc.def
			got := argvFor(&def, "M", []string{"--x"}, "PROMPT")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestArgvForOmitsEmptyFields pins the no-model, no-passthrough shape: no
// empty argument is ever handed to a binary, with or without PromptFlag.
func TestArgvForOmitsEmptyFields(t *testing.T) {
	tests := []struct {
		def  agentDef
		want []string
	}{
		{
			def:  agentDef{Name: "codex", Binary: "codex", Args: []string{"exec"}, ModelFlag: modelFlag},
			want: []string{"codex", "exec", "PROMPT"},
		},
		{
			def:  agentDef{Name: "amp", Binary: "amp", PromptFlag: "-x", ModelFlag: modelFlag},
			want: []string{"amp", "-x", "PROMPT"},
		},
	}
	for _, tc := range tests {
		def := tc.def
		if got := argvFor(&def, "", nil, "PROMPT"); !slices.Equal(got, tc.want) {
			t.Errorf("%s argv = %q, want %q", def.Name, got, tc.want)
		}
	}
}
