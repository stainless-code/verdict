package backtest

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// An --agent name is quoted back when no agent row carries it.
func TestReplayAgentCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, replayAgent(forgedline.Value), "unknown agent '"+forgedline.Named+"'")
}
