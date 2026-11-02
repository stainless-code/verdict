// Package verdict is the public Go facade over the review core services.
//
// Experimental: the Go API may change in a minor release before 1.0; pin a
// version if you embed. The stable contract is the binary's --json envelope
// plus the published JSON Schemas (schema_version "1"): the core owns every
// behavior, this package only converts typed requests and responses.
//
// Bundle files resolve from the process working directory, exactly like the
// CLI: call from (or change into) the checkout that carries .review/.
// Options.Dir anchors tool-version and provenance lookups to that checkout.
// Methods are context-first, never call [os.Exit], and never touch process
// stdio: human lines and warnings flow through Options.Progress.
//
// Distribution surfaces (install, upgrade, skill) stay CLI-only on purpose
// and are not exposed here.
package verdict
