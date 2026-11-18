# Verdict

Verdict is a local, agent-driven pull-request review toolkit: it hands a review agent the change, validates what the agent returns, and keeps an auditable trail.

## Guidance

**Procedure**:
The review instructions Verdict gives an agent: what to read, how to judge, how to tag severity and rigor, and how to self-check before recording. Only the Verdict binary serves it, from its own embedded copy, so it always matches the binary in use.
_Avoid_: skill content, prompts, instructions

**Bootstrap skill**:
The small skill file installed into an agent harness. It names when to use Verdict and how to reach the Procedure through the binary, and carries no Procedure itself.
_Avoid_: skill copy, installed skill (when it means the Procedure)

## Review

**Bundle**:
Everything a single review reads, written under `.verdict/state/<target>/`: the change, its metadata, the handed context, the controls, and the Procedure for that review.
_Avoid_: workspace, state dir, snapshot
