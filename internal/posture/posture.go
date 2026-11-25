// Package posture is the one write-posture rule behind the three long-running
// local surfaces: `verdict mcp`, `verdict serve`, and `verdict lsp`.
//
// Local writes (triage, KB promotion, record, adopt, fix, verify — anything
// that rewrites `.verdict/` state) are allowed unless the surface was started
// with --read-only. --read-only is the only knob for them. Posting to a code
// host is a stronger trust decision: it needs the local-writes answer plus
// whatever separate consent the surface demands (`serve --allow-post`), and
// --read-only forbids it outright. A surface that has no write path at all
// (lsp) answers no to both for every input.
package posture

// FlagReadOnly is the one write-posture flag every surface accepts.
const FlagReadOnly = "--read-only"

// Rule is one surface's resolved write posture. The zero value is the default
// posture: local writes allowed, host posting not opted into.
type Rule struct {
	// ReadOnly is --read-only: no local write and no host post.
	ReadOnly bool
	// HostPostOptIn is the surface's separate consent to post to a code host:
	// `serve --allow-post`. A surface with no separate flag (mcp, where the
	// tool is simply exposed) sets it true; one with no post path leaves it
	// false.
	HostPostOptIn bool
}

// LocalWrites reports whether the surface may write local state.
func (r Rule) LocalWrites() bool { return !r.ReadOnly }

// HostPost reports whether the surface may post to a code host: local writes
// allowed and the surface's own opt-in given. Read-only always wins.
func (r Rule) HostPost() bool { return !r.ReadOnly && r.HostPostOptIn }

// RetiredFlagNotice is the one-line stderr notice for a flag the shared rule
// retired but a script may still pass: it is accepted for one release and
// ignored.
func RetiredFlagNotice(surface, flag string) string {
	return surface + ": " + flag + " is deprecated and ignored: local writes are allowed unless " +
		FlagReadOnly + " (host posting stays separately gated); the flag goes away in the next release"
}
