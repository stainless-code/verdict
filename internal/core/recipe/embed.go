package recipe

import _ "embed"

// builtinDefault is the embedded pipeline set a run falls back to. It is the
// same strict document format a repository file uses, so the fallback cannot
// drift from what the loader accepts.
//
//go:embed builtin/default.json
var builtinDefault []byte
