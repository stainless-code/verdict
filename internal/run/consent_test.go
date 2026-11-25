package run

import "testing"

func TestConsentFlagReachesRequestAndRentry(t *testing.T) {
	runner, _, _ := testRunner()
	flags, code, done := parseFlags(runner, []string{"--agent", "claude", "--consent-over-budget"})
	if done || code != 0 || !flags.consent {
		t.Fatalf("consent parse = %+v, %d, %t", flags, code, done)
	}
	request := runRequest(runner, testReq(), &flags)
	if !request.ConsentOverBudget {
		t.Fatal("consent lost at service boundary")
	}
	if got := rentry(testReq(), &flags, false); got != "verdict run acme/app 42 --agent claude --consent-over-budget" {
		t.Fatalf("rentry = %q", got)
	}
}

func TestMissingPersonalSettingsKeepCapturedSpend(t *testing.T) {
	_, policy, ref := personalAgents(map[string]string{"XDG_CONFIG_HOME": t.TempDir()})
	if ref != nil || policy != nil {
		t.Fatalf("missing settings override capture with policy %+v, refusal %v", policy, ref)
	}
}
