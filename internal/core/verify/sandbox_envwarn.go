package verifycore

import (
	"slices"
	"strings"
)

// procEnvExposed reports whether a sandbox mechanism leaves other same-uid
// processes' environments readable to the command — verdict's own included.
// sandbox-exec cannot gate the kern.procargs2 sysctl, and the network-only
// mode keeps the host /proc; bwrap's --unshare-pid and fresh /proc hide them.
func procEnvExposed(mechanism string) bool {
	return mechanism == sandboxMechanismSandboxExec || mechanism == sandboxMechanismUnshareNet
}

// secretEnvNames lists, sorted and once each, the variables in environ the
// tail redactor treats as secrets: a secret-shaped name with a value of at
// least secretMinLen bytes. Values never leave this function.
func secretEnvNames(environ []string) []string {
	var names []string
	for _, kv := range environ {
		if name, _, ok := secretEntry(kv); ok && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// exposedSecretsWarning is the one-line warning for a sandboxed run whose
// mechanism exposes verdict's own environment while it carries secrets, or
// "" when either half is absent. It names the variables, never their values.
func exposedSecretsWarning(mechanism string, environ []string) string {
	if !procEnvExposed(mechanism) {
		return ""
	}
	names := secretEnvNames(environ)
	if len(names) == 0 {
		return ""
	}
	why := "sandbox-exec cannot hide other processes' environments from the command (macOS kern.procargs2)"
	if mechanism == sandboxMechanismUnshareNet {
		why = "the network-only sandbox leaves the host /proc readable to the command"
	}
	return why + ", and verdict's own environment carries " + strings.Join(names, ", ") +
		" — unset them before verifying a hostile PR, or run the bwrap recipe in a Linux VM " +
		"(docs/manual.md, Hostile-PR sandbox recipe)"
}
