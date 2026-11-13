package ready

import (
	"testing"

	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func TestParseBuildWait(t *testing.T) {
	q := &readycore.Request{}
	err := parseFlags([]string{"--check", "--wait-for", "build", "--timeout", "20m"}, q)
	if err != nil || !q.Check || q.WaitFor != "build" || q.Timeout != "20m" {
		t.Fatalf("request=%+v err=%v", q, err)
	}
}

func TestParseBuildWaitRejectsMissingOperand(t *testing.T) {
	if err := parseFlags([]string{"--timeout"}, &readycore.Request{}); err == nil {
		t.Fatal("missing operand accepted")
	}
}
