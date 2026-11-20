package run

import "testing"

func TestConsentFlagReachesRequestAndRentry(t *testing.T) {
	runner, _, _ := testRunner()
	flags, code, done := parseFlags(runner, []string{"--agent", "claude", "--consent-over-budget"})
	if done || code != 0 || !flags.consent {
		t.Fatalf("consent parse = %+v, %d, %t", flags, code, done)
	}
	request := runRequest(runner, Request{Repo: "acme/app", Pr: "42"}, &flags)
	if !request.ConsentOverBudget {
		t.Fatal("consent lost at service boundary")
	}
	if got := rentry(Request{Repo: "acme/app", Pr: "42"}, &flags, false); got != "verdict run acme/app 42 --agent claude --consent-over-budget" {
		t.Fatalf("rentry = %q", got)
	}
}
