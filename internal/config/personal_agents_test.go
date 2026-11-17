package config

import (
	"slices"
	"strings"
	"testing"
)

// TestParsePersonalAgents pins the operator-defined agent shape: the binary is
// required, args defaults to empty, the other fields are optional strings, and
// the definitions are returned in name order for stable discovery.
func TestParsePersonalAgents(t *testing.T) {
	give := `{"agents":{
		"z-local":{"binary":"local-agent","args":["exec","-q"],"prompt_flag":"-p",
			"model_flag":"-m","install":"brew install local-agent"},
		"a-min":{"binary":"/usr/local/bin/min"}
	}}`
	got, ref := ParsePersonal([]byte(give))
	if ref != nil {
		t.Fatalf("ParsePersonal: %v", ref)
	}
	if len(got.Agents) != 2 {
		t.Fatalf("agents = %+v, want two rows", got.Agents)
	}
	assertMinimalAgent(t, &got.Agents[0])
	assertFullAgent(t, &got.Agents[1])
}

func assertMinimalAgent(t *testing.T, row *PersonalAgent) {
	t.Helper()
	if row.Name != "a-min" || row.Binary != "/usr/local/bin/min" ||
		len(row.Args) != 0 || row.PromptFlag != "" || row.ModelFlag != "" {
		t.Errorf("row = %+v, want the defaults-filled minimal row", row)
	}
}

func assertFullAgent(t *testing.T, row *PersonalAgent) {
	t.Helper()
	if row.Name != "z-local" || row.Binary != "local-agent" ||
		!slices.Equal(row.Args, []string{"exec", "-q"}) ||
		row.PromptFlag != "-p" || row.ModelFlag != "-m" ||
		row.Install != "brew install local-agent" {
		t.Errorf("row = %+v, want every field read verbatim", row)
	}
}

// TestPersonalAgentsRefusals pins every closed-shape rule. Each refusal names
// the prefs key and the offending field, and reuses bad-settings so the CLI
// renders it with the settings remediation.
func TestPersonalAgentsRefusals(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{
			name: "unknown agent key",
			give: `{"agents":{"local":{"binary":"local","promt_flag":"-p"}}}`,
			want: "$.agents.local.promt_flag",
		},
		{
			name: "empty binary",
			give: `{"agents":{"local":{"binary":"  "}}}`,
			want: "$.agents.local.binary",
		},
		{
			name: "missing binary",
			give: `{"agents":{"local":{"install":"brew install local"}}}`,
			want: "$.agents.local.binary",
		},
		{
			name: "relative binary with a separator",
			give: `{"agents":{"local":{"binary":"./local"}}}`,
			want: "$.agents.local.binary",
		},
		{
			name: "built-in collision",
			give: `{"agents":{"claude":{"binary":"other"}}}`,
			want: "$.agents.claude",
		},
		{
			name: "uppercase name",
			give: `{"agents":{"Local":{"binary":"local"}}}`,
			want: "$.agents.Local",
		},
		{
			name: "leading dash name",
			give: `{"agents":{"-local":{"binary":"local"}}}`,
			want: "$.agents.-local",
		},
		{
			name: "empty name",
			give: `{"agents":{"":{"binary":"local"}}}`,
			want: "$.agents.",
		},
		{
			name: "wrong agents shape",
			give: `{"agents":[]}`,
			want: "$.agents",
		},
		{
			name: "wrong binary type",
			give: `{"agents":{"local":{"binary":3}}}`,
			want: "$.agents.local.binary",
		},
		{
			name: "wrong args type",
			give: `{"agents":{"local":{"binary":"local","args":"exec"}}}`,
			want: "$.agents.local.args",
		},
		{
			name: "wrong optional string type",
			give: `{"agents":{"local":{"binary":"local","prompt_flag":true}}}`,
			want: "$.agents.local.prompt_flag",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertAgentRefusal(t, tc.give, tc.want) })
	}
}

func assertAgentRefusal(t *testing.T, give, want string) {
	t.Helper()
	_, ref := ParsePersonal([]byte(give))
	if ref == nil || ref.Code != "bad-settings" {
		t.Fatalf("refusal = %+v, want bad-settings", ref)
	}
	if !strings.Contains(ref.Message, want) {
		t.Errorf("message = %q, want it to name %q", ref.Message, want)
	}
}

// TestAgentsArePrefsOnly pins the boundary: the same key in a repository
// configuration refuses as an unknown field, so a PR cannot define an agent.
func TestAgentsArePrefsOnly(t *testing.T) {
	_, ref := Parse([]byte(`{"schema_version":"1","agents":{"local":{"binary":"local"}}}`))
	if ref == nil || ref.Code != CodeBadConfig {
		t.Fatalf("repo config refusal = %+v, want bad-config", ref)
	}
	if !strings.Contains(ref.Message, "agents") {
		t.Errorf("message = %q, want it to name the agents key", ref.Message)
	}
}
