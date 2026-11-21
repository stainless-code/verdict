package budget

import "slices"

// builtinSensitivePathGlobs is the default high-risk path list, in match order.
// Each entry names a directory tree whose change is worth the deepest pass
// Verdict can run: authentication and authorization, security policy,
// migrations, billing, and the CI workflows that decide what else may merge.
//
// A repository extends this list through defaults.sensitive_paths; it can never
// remove an entry, because the extend-only direction can only buy review depth
// and never take it away from an auth diff.
var builtinSensitivePathGlobs = []string{
	"**/auth/**",
	"**/security/**",
	"**/migrations/**",
	"**/billing/**",
	".github/workflows/**",
}

// BuiltinSensitive returns the built-in sensitive-path globs in match order.
// The caller owns the slice: it extends with the repository's globs and never
// mutates the built-in list.
func BuiltinSensitive() []string { return slices.Clone(builtinSensitivePathGlobs) }
