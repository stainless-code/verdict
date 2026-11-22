# Security lens

Dispatch only when the diff changes a trust boundary: where identity comes
from, what reaches an interpreter or the network, or what bytes are decoded
into objects. Frame one falsifiable question — who controls this value, what
does it reach, what does the attacker gain — and give it a short hypothesis
label.

Read the changed files and the callers that supply the value; the on-disk
diff is authoritative scope. Scope the question to at most ~12 files, cite
NEW line numbers, and trace the value from request to sink. An unproven
claim stays a question, never a finding.
