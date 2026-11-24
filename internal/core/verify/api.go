package verifycore

import "github.com/stainless-code/verdict/internal/core"

// Request is the verify service invocation: the read runtime and
// positionals, the raw --timeout value (so bad-timeout stays a typed
// refusal at exit 1), the ordered process-environ snapshot the child
// inherits (order preserved like [os.Environ]), and whether --json mode
// defaults NO_COLOR on for the child. Commander arrives via the runtime;
// tests inject a scripted one, the adapter installs the production
// watchdog.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	TimeoutRaw string
	Environ    []string
	JSONMode   bool
	Warnings   []string
	// Group selects deep group-validation mode: the stable PlanGroup.ID
	// whose validation this run records, with Command the explicit
	// operator-supplied command to execute. Both are required together;
	// neither keeps the ordinary patch.json verify path, which never reads
	// these fields.
	Group   string
	Command string
	// Sandbox wraps the executed command in the host's enforced sandbox:
	// bwrap on Linux (no network, empty credential dirs, a writable reviewed
	// root, a private /tmp) and sandbox-exec on macOS (no network, no Mach
	// services, writes only to the reviewed root and a private TMPDIR, denied
	// credential paths and shared host temp roots). A host that cannot
	// enforce it, or a reviewed root whose shape it cannot protect (Linux: no
	// .git, a symlinked .git, or a git pointer that does not resolve), refuses
	// sandbox-unavailable instead of running unwrapped; it never falls back
	// to the network-only opt-in.
	Sandbox bool
	// SandboxNetworkOnly is the explicit Linux fallback for hosts without
	// bubblewrap: a user and network namespace from unshare, and nothing else.
	// It provides no mount, filesystem, or PID isolation — credential paths,
	// /tmp, the host /proc, and filesystem unix sockets stay reachable — and
	// every run says so in its warnings and records sandboxMechanism
	// "unshare-net" without `sandboxed`. It refuses sandbox-unavailable on
	// every other host and is mutually exclusive with Sandbox (bad-flags).
	SandboxNetworkOnly bool
	// accountHome replaces the passwd-database lookup of the home the sandbox
	// also hides the credential table under. It is unexported on purpose: no
	// adapter (CLI, MCP, serve, the Go facade) can point it elsewhere, and
	// only tests pin it through SetAccountHomeForTest.
	accountHome string
	// EnvAllowlist explicitly permits additional child environment variable
	// names (even if secret-named) on top of the default allowlist and any
	// base-snapshotted .verdict/verify-env entries.
	EnvAllowlist []string
}

// Response carries the verify outcome: the envelope data, warnings,
// the next step, and the recorded patch fields. Human lines stream live
// through the runtime progress observer (they interleave with warnings on
// the diagnostic stream), so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *core.VerifyData
	Warnings []string
	Next     string
	Exit     int
	TimedOut bool
	Tail     string
	PatchDir string
}
