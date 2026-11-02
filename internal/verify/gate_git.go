package verify

import (
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
)

// checkDestructiveGit ports the git-config/hook arms: -c keys, env config
// vectors, HOME overrides with git, ext transports, clone vectors, and the
// core.pager narrowing.
func checkDestructiveGit(g *gateText) *cli.RefusalError {
	if ref := checkGitHookKeys(g); ref != nil {
		return ref
	}
	if ref := checkGitEnvConfig(g); ref != nil {
		return ref
	}
	if ref := checkGitHomeOverride(g); ref != nil {
		return ref
	}
	if ref := checkGitExtTransport(g); ref != nil {
		return ref
	}
	if ref := checkGitCloneVectors(g); ref != nil {
		return ref
	}
	return checkGitPager(g)
}

// checkGitHomeOverride ports the HOME/XDG_CONFIG_HOME-with-git arm.
func checkGitHomeOverride(g *gateText) *cli.RefusalError {
	if !reGitWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reGitHomeOverride, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			"HOME/XDG_CONFIG_HOME override with a git command reads planted hook configs — run git with the default home")
	}
	return nil
}

// checkGitExtTransport ports the protocol.ext.allow / ext:: transport arm.
func checkGitExtTransport(g *gateText) *cli.RefusalError {
	if !reProtoExtAllow.MatchString(g.bare) &&
		(!reGitWord.MatchString(g.bare) || !strings.Contains(g.bare, "ext::")) {
		return nil
	}
	if frag := grepO(reProtoExtFrag, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git ext transport executes a local command — fetch fixtures first, run offline")
	}
	return nil
}

// checkGitCloneVectors ports the --upload-pack / --config-env arms.
func checkGitCloneVectors(g *gateText) *cli.RefusalError {
	if !reGitWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reGitUploadPack, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git upload-pack executes a local command — clone without --upload-pack")
	}
	if frag := grepO(reGitConfigEnv, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git --config-env sets a hook key from the command line — fetch fixtures first, run offline")
	}
	return nil
}

// checkGitPager ports the core.pager narrowing: known-safe pagers pass,
// anything else refuses.
func checkGitPager(g *gateText) *cli.RefusalError {
	if !reCorePagerSet.MatchString(g.bare) {
		return nil
	}
	pgval := grepO(reCorePagerVal, g.bare)
	pgval = reCorePagerStrip.ReplaceAllString(pgval, "")
	switch lastPathElement(pgval) {
	case "cat", "less", "more", "true", "fold", "":
	default:
		return refuse(codeDestructive, "core.pager="+pgval, "git pager executes commands — use core.pager=cat")
	}
	return nil
}

// checkGitHookKeys ports the -c/space-form hook key arms.
func checkGitHookKeys(g *gateText) *cli.RefusalError {
	if frag := grepO(reGitFsmonitor, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git fsmonitor hook executes commands — set it in repo config, not -c")
	}
	if frag := grepO(reGitSSHCommand, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git sshcommand hook executes commands — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitAskpass, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git askpass hook executes commands — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitCredHelper, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "git credential-helper shellout executes commands — use store/cache")
	}
	return nil
}

// checkGitEnvConfig ports the GIT_* env config arms (refused outright).
func checkGitEnvConfig(g *gateText) *cli.RefusalError {
	if frag := grepO(reGitConfigCount, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			"GIT_CONFIG_COUNT sets git config from the environment — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitConfigKV, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			"GIT_CONFIG_KEY/VALUE sets git config from the environment — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitSSH, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			"GIT_SSH_COMMAND replaces ssh with an attacker command — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitAskpassEnv, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "GIT_ASKPASS runs an attacker command on auth — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitExtDiff, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "GIT_EXTERNAL_DIFF runs an attacker command on diff — fetch fixtures first, run offline")
	}
	if frag := grepO(reGitPagerEnv, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "GIT_PAGER runs an attacker command on pager use — use core.pager=cat")
	}
	return nil
}

// lastPathElement ports `${pgval##*/}` (empty when pgval is empty).
func lastPathElement(pgval string) string {
	if _, after, ok := strings.CutLast(pgval, "/"); ok {
		return after
	}
	return pgval
}

// Git-hook patterns, transliterated from the shell grep arms.
var (
	reGitFsmonitor    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])core\.fsmonitor([[:space:]]*=|[[:space:]]+[^[:space:];|&])`)
	reGitSSHCommand   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])core\.sshcommand([[:space:]]*=|[[:space:]]+[^[:space:];|&])`)
	reGitAskpass      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])core\.askpass([[:space:]]*=|[[:space:]]+[^[:space:];|&])`)
	reGitCredHelper   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])credential\.helper([[:space:]]*=[[:space:]]*|[[:space:]]+)!`)
	reGitConfigCount  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_config_count[[:space:]]*=`)
	reGitConfigKV     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_config_(key|value)_[0-9]+[[:space:]]*=`)
	reGitSSH          = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_ssh(_command)?[[:space:]]*=`)
	reGitAskpassEnv   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_askpass[[:space:]]*=`)
	reGitExtDiff      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_external_diff[[:space:]]*=`)
	reGitPagerEnv     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git_pager[[:space:]]*=`)
	reGitWord         = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])git([[:space:];|&]|$)`)
	reGitHomeOverride = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(home|xdg_config_home)[[:space:]]*=`)
	reProtoExtAllow   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])protocol\.ext\.allow`)
	reProtoExtFrag    = regexp.MustCompile(`protocol\.ext\.allow|ext::[^[:space:];|&]*`)
	reGitUploadPack   = regexp.MustCompile(`--upload-pack([^a-z-]|$)`)
	reGitConfigEnv    = regexp.MustCompile(`--config-env[[:space:]=][^;|&]*(fsmonitor|sshcommand|askpass|helper|pager|ext\.allow|upload)`)
	reCorePagerSet    = regexp.MustCompile(`core\.pager[[:space:]]*=`)
	reCorePagerVal    = regexp.MustCompile(`core\.pager[[:space:]]*=[[:space:]]*[^[:space:];|&]*`)
	reCorePagerStrip  = regexp.MustCompile(`^core\.pager[[:space:]]*=[[:space:]]*`)
)
