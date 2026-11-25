package verifycore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/envsecret"
)

// maxNamedSecrets caps the names one warning lists; the rest are counted.
const maxNamedSecrets = 10

// procEnvExposed reports whether a sandbox mechanism leaves other same-uid
// processes' environments readable to the command — verdict's own included.
// sandbox-exec cannot gate the kern.procargs2 sysctl, and the network-only
// mode keeps the host /proc; bwrap's --unshare-pid and fresh /proc hide them.
func procEnvExposed(mechanism string) bool {
	return mechanism == sandboxMechanismSandboxExec || mechanism == sandboxMechanismUnshareNet
}

// secretEnvNames lists, sorted and once each, the variables in environ the
// tail redactor treats as secrets: a secret-shaped name with a value of at
// least secretMinLen bytes. A name outside the portable env-name shape is
// counted in malformed instead of listed, so a name carrying a line break
// or a terminal escape never reaches the warning. Values never leave here.
func secretEnvNames(environ []string) (names []string, malformed int) {
	for _, kv := range environ {
		name, _, ok := secretEntry(kv)
		switch {
		case !ok:
		case !envNameShaped(name):
			malformed++
		case !slices.Contains(names, name):
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, malformed
}

// envNameShaped reports whether name is [A-Za-z_][A-Za-z0-9_]*, the shape a
// shell can export: plain ASCII with no control or separator characters.
func envNameShaped(name string) bool {
	return name != "" && strings.IndexByte(envNameDigits, name[0]) < 0 && strings.Trim(name, envNameChars) == ""
}

// envNameDigits and envNameChars spell the portable env-name alphabet.
const (
	envNameDigits = "0123456789"
	envNameChars  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_" + envNameDigits
)

// exposedSecretsWarning is the one-line warning for a sandboxed run whose
// mechanism exposes verdict's own environment while it carries secrets, or
// "" when either half is absent. It names the variables, never their values.
func exposedSecretsWarning(mechanism string, environ []string) string {
	if !procEnvExposed(mechanism) {
		return ""
	}
	names, malformed := secretEnvNames(environ)
	if len(names) == 0 && malformed == 0 {
		return ""
	}
	why := "sandbox-exec cannot hide other processes' environments from the command (macOS kern.procargs2)"
	if mechanism == sandboxMechanismUnshareNet {
		why = "the network-only sandbox leaves the host /proc readable to the command"
	}
	return why + ", and verdict's own environment carries " + describeNames(names, malformed, environ) +
		" — unset them before verifying a hostile PR, or run the bwrap recipe in a Linux VM " +
		"(docs/manual.md, Hostile-PR sandbox recipe)"
}

// describeNames renders at most maxNamedSecrets names, each through the
// value-shape redactor (a name can itself embed a token, e.g. ghp_…_TOKEN),
// then counts what it left out.
func describeNames(names []string, malformed int, environ []string) string {
	shown := names[:min(len(names), maxNamedSecrets)]
	parts := make([]string, 0, len(shown)+2)
	for _, name := range shown {
		// A name can equal another variable's secret value, so scrub those
		// values too, not only the provider token shapes.
		parts = append(parts, envsecret.ScrubShapes(redactShapes(redactSecrets(name, environ))))
	}
	if more := len(names) - len(shown); more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	if malformed > 0 {
		parts = append(parts, fmt.Sprintf("and %d malformed name(s)", malformed))
	}
	return strings.Join(parts, ", ")
}
