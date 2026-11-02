package verifycore

import (
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// cmdPosBuilds prefixes the command-position refusal details.
const cmdPosBuilds = "command substitution in command position builds a "

// segWordIdx selects the first-word capture in the segWords pattern.
const segWordIdx = 4

// checkSentinel ports the mid-word substitution arm: a sentinel glued to a
// word character means the shell concatenates it into a hidden tool word.
func checkSentinel(g *gateText) *core.RefusalError {
	if !reSentinel.MatchString(g.adj) {
		return nil
	}
	frag := strings.ReplaceAll(grepO(reSentinelFrag, g.adj), markStr, "?")
	if frag == "" {
		frag = "mid-word-substitution"
	}
	return refuse(codeDestructive, frag, "mid-word substitution concatenates into a hidden tool word — write the tool word plainly")
}

// checkCommandPosition ports the command-position substitution arms: a
// $(…)/${…}/backtick span at segment start executes with the body's tool
// word as argv[0].
func checkCommandPosition(g *gateText) *core.RefusalError {
	if frag := grepO(reCmdPosDest, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			cmdPosBuilds+
				"destructive tool word — write the tool word plainly")
	}
	if frag := grepO(reCmdPosNet, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			cmdPosBuilds+
				"networked tool word — fetch fixtures first, run offline")
	}
	if frag := grepO(reCmdPosDestBt, g.scan); frag != "" {
		return refuse(codeDestructive, frag,
			cmdPosBuilds+
				"destructive tool word — write the tool word plainly")
	}
	if frag := grepO(reCmdPosNetBt, g.scan); frag != "" {
		return refuse(codeNetworked, frag,
			cmdPosBuilds+
				"networked tool word — fetch fixtures first, run offline")
	}
	return nil
}

// checkGlobFragment ports the glob/brace fragmentation arm: the first word
// of each ;-separated segment (past time/command prefixes and VAR=
// assignments) refuses when it carries ? * [ {.
func checkGlobFragment(g *gateText) *core.RefusalError {
	segw := segWords(g.bare)
	if !hasGlobWord(segw) {
		return nil
	}
	frag := globFrag(segw)
	if frag == "" {
		frag = "globbed-command-word"
	}
	return refuse(codeDestructive, frag,
		"glob metacharacters in command position expand into a hidden tool word — write the tool word plainly")
}

// hasGlobWord reports whether any segment word (outside the exact
// test/grouping forms) carries a glob metacharacter.
func hasGlobWord(segw []string) bool {
	for _, w := range segw {
		if isBareBracket(w) {
			continue
		}
		if reGlobChars.MatchString(w) {
			return true
		}
	}
	return false
}

// globFrag returns the first glob-bearing word's matched fragment.
func globFrag(segw []string) string {
	for _, w := range segw {
		if isBareBracket(w) {
			continue
		}
		if frag := grepO(reGlobFrag, w); frag != "" {
			return frag
		}
	}
	return ""
}

// isBareBracket reports the exact `[`, `[[`, and `{` forms, which stay
// allowed (test and grouping), unlike glob metacharacters in command
// position.
func isBareBracket(w string) bool {
	return w == "[" || w == "[[" || w == "{"
}

// segWords ports the segw pipeline: split on ;|& and take the first word
// past prefixes, exactly like the shell sed.
func segWords(bare string) []string {
	parts := strings.Split(bare, ";")
	var segs []string
	for _, p := range parts {
		segs = append(segs, strings.Split(p, "|")...)
	}
	var amp []string
	for _, s := range segs {
		amp = append(amp, strings.Split(s, "&")...)
	}
	out := make([]string, 0, len(amp))
	for _, seg := range amp {
		if m := reSegWord.FindStringSubmatch(seg); m != nil {
			out = append(out, m[segWordIdx])
			continue
		}
		out = append(out, seg)
	}
	return out
}

// checkDestructive runs the destructive-class arms in shell order.
func checkDestructive(g *gateText) *core.RefusalError {
	if ref := checkDestructiveWords(g); ref != nil {
		return ref
	}
	if ref := checkDestructiveExec(g); ref != nil {
		return ref
	}
	if ref := checkDestructiveEnv(g); ref != nil {
		return ref
	}
	return checkDestructiveGit(g)
}

// checkDestructiveWords ports the rm/find/mkfs/shred/dd/forkbomb/chmod
// arms plus the git-clean shape.
func checkDestructiveWords(g *gateText) *core.RefusalError {
	if ref := checkRmFind(g); ref != nil {
		return ref
	}
	if ref := checkDeviceWipe(g); ref != nil {
		return ref
	}
	if ref := checkChmodClean(g); ref != nil {
		return ref
	}
	return nil
}

// checkRmFind ports the rm/find destructive arms.
func checkRmFind(g *gateText) *core.RefusalError {
	if frag := grepO(reRmRecursive, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "rm with a recursive flag destroys without recovery")
	}
	if frag := grepO(reFindDelete, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "find -delete destroys without recovery — treat as rm")
	}
	if frag := grepO(reFindExecRm, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "find -exec rm destroys without recovery — treat as rm")
	}
	return nil
}

// checkDeviceWipe ports the mkfs/shred/dd/forkbomb arms.
func checkDeviceWipe(g *gateText) *core.RefusalError {
	if frag := grepO(reMkfs, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "mkfs destroys filesystems")
	}
	if frag := grepO(reShred, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "shred destroys files without recovery — treat as rm")
	}
	if frag := grepO(reDdDevice, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "dd writing a device destroys without recovery")
	}
	if frag := grepO(reForkBomb, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "fork-bomb shape")
	}
	return nil
}

// checkChmodClean ports the chmod/chown and git-clean arms.
func checkChmodClean(g *gateText) *core.RefusalError {
	if frag := grepO(reChmodRecursive, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "recursive chmod/chown refuses regardless of target — fail closed")
	}
	if seg := grepO(reGitClean, g.bare); seg != "" {
		if gitCleanDestructive(seg) {
			return refuse(codeDestructive, seg, "git clean -f with a / or ~ pathspec destroys outside the repo")
		}
	}
	return nil
}

// gitCleanDestructive reports whether a git-clean segment forces removal
// outside the repo.
func gitCleanDestructive(seg string) bool {
	return strings.Contains(seg, "-f") && (strings.Contains(seg, "~") ||
		strings.Contains(seg, "$home") || strings.Contains(seg, " /"))
}

// checkDestructiveExec ports the privilege/eval/exec arms.
func checkDestructiveExec(g *gateText) *core.RefusalError {
	if frag := grepO(reSudo, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "privilege change refuses regardless of payload")
	}
	if frag := grepO(reSu, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "privilege change refuses regardless of payload")
	}
	if frag := grepO(reEval, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "eval with non-literal args refuses — fail closed")
	}
	if frag := grepO(reExecParen, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "exec( with non-literal args refuses — fail closed")
	}
	if frag := grepO(reTarExec, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "tar executes the named command — archive without exec options")
	}
	return nil
}

// checkDestructiveEnv ports the loader-hook and NODE_OPTIONS arms.
func checkDestructiveEnv(g *gateText) *core.RefusalError {
	if frag := grepO(reLoaderHook, g.bare); frag != "" {
		return refuse(codeDestructive, frag,
			"env-prefix loader hook executes code at startup — do not set loader variables in verifyCmd")
	}
	if frag := grepO(reNodePreload, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "NODE_OPTIONS preload hook executes code at startup — use plain flags instead")
	}
	if frag := grepO(reNodeDashR, g.bare); frag != "" {
		return refuse(codeDestructive, frag, "NODE_OPTIONS -r preload hook executes code at startup — use plain flags instead")
	}
	return nil
}

// Destructive-class patterns, transliterated from the shell grep arms.
var (
	reSentinel     = regexp.MustCompile("[\x01][a-z0-9_]|[a-z0-9_][\x01]")
	reSentinelFrag = regexp.MustCompile(".{0,10}\x01.{0,10}")

	q1pre = `(^|[;|&{])[[:space:]]*((command|time)[[:space:]]+)?([a-z0-9_]+=[^[:space:];|&]+[[:space:]]+)*`

	reCmdPosDest = regexp.MustCompile(q1pre + `(\$\(|\$\{)([^;|&]*[^a-z_])?(rm|find|mkfs|dd|shred|chmod|` +
		`chown|sudo|doas|su|runuser|eval|exec|git|tar)([^a-z_]|$)`)
	reCmdPosNet = regexp.MustCompile(q1pre +
		`(\$\(|\$\{)([^;|&]*[^a-z_])?(curl|wget|sh|bash|zsh|fish|dash|ssh|scp|` +
		`sftp|ftp|tftp|lftp|nc|ncat|netcat|socat|openssl|ping|dig|nslookup|gh|` +
		`rsync|busybox|docker|podman|nerdctl|env|printenv|whoami|id|uname|` +
		`hostname|groups|xargs|parallel)([^a-z_]|$)`)
	reCmdPosDestBt = regexp.MustCompile(q1pre +
		"`([^`;|&]*[^a-z_])?(rm|find|mkfs|dd|shred|chmod|chown|sudo|doas|su|runuser|eval|exec|git|tar)([^a-z_]|`|$)")
	reCmdPosNetBt = regexp.MustCompile(q1pre +
		"`([^`;|&]*[^a-z_])?(curl|wget|sh|bash|zsh|fish|dash|ssh|scp|sftp|ftp|tftp|lftp|nc|ncat|netcat|socat|openssl|ping|" +
		"dig|nslookup|gh|rsync|busybox|docker|podman|nerdctl|env|printenv|whoami|id|uname|hostname|groups|xargs|parallel)([^a-z_]|`|$)")

	reSegWord   = regexp.MustCompile(`^[[:space:]]*((command|time)[[:space:]]+)?([a-z0-9_]+=[^[:space:]]+[[:space:]]+)*([^[:space:]]+)`)
	reGlobChars = regexp.MustCompile(`[?*\[{]`)
	reGlobFrag  = regexp.MustCompile(`[^[:space:]]*[?*\[{][^[:space:]]*`)

	reRmRecursive = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])rm[[:space:]]+(-[a-z]*r[a-z]*|--recursive)`)
	reFindDelete  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])find[[:space:]]+[^;|&]*-delete([^a-z]|$)`)
	reFindExecRm  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])find[[:space:]]+[^;|` +
		`&]*-exec(dir)?[[:space:]]+([^[:space:];|&]*/)?rm([[:space:];|&]|$)`)
	reMkfs           = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])mkfs[a-z0-9._-]*`)
	reShred          = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])shred([[:space:];|&]|$)`)
	reDdDevice       = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])dd[[:space:]]+[^;|&]*[[:space:]]of=/dev/`)
	reForkBomb       = regexp.MustCompile(`:[[:space:]]*\(\)`)
	reChmodRecursive = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])ch(mod|own)[[:space:]]+(-[a-z]*r[a-z]*|--recursive)`)
	reGitClean       = regexp.MustCompile(`git[[:space:]]+clean[^;|&]*`)
	reSudo           = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(sudo|doas|runuser)([[:space:];|&]|$)`)
	reSu             = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])su([[:space:];|&]|$)`)
	reEval           = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])eval([[:space:];|&]|$)`)
	reExecParen      = regexp.MustCompile(`exec[[:space:]]*\(`)
	reLoaderHook     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(ld_preload|dyld_insert_libraries|` +
		`bash_env|zdotdir|perl5opt|rubyopt)[[:space:]]*=`)
	reNodePreload = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])node_options[^;|&]*--(require|` +
		`import|experimental-loader|loader)([^a-z-]|$)`)
	reNodeDashR = regexp.MustCompile(`(^|[[:space:];|&(){}"=,:/-])node_options[^;|&]*-r([^a-z0-9-]|$)`)
	reTarExec   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(g?tar)[[:space:]]+[^;|` +
		`&]*--(checkpoint-action|use-compress-program|to-command)([^a-z-]|$)`)
)
