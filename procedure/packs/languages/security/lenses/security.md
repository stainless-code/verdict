# Security lens

Dispatch only when the diff changes a trust boundary: where identity comes
from, what reaches an interpreter or the network, what bytes are decoded into
objects, how a secret is compared or generated, or how a name becomes a file
path. Frame one falsifiable question — who controls this value, what does it
reach, what does the attacker gain — and give it a short hypothesis label.

Read the changed files and the callers that supply the value; the on-disk diff
is authoritative scope. Trace the value from request to sink, and state the
framework/version fact the finding depends on (Go follows ten redirects by
default; `crypto.timingSafeEqual` requires equal-length buffers; `math/rand`
is not a CSPRNG even in `math/rand/v2`). Scope the question to at most ~12
files, cite NEW line numbers. An unproven claim stays a question, never a
finding.
