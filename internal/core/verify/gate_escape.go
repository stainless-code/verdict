package verifycore

import (
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/core"
)

// checkRepoEscape ports the verify-repo-escape arms: device/proc/sys
// access, dot-dot traversal, tilde/home targets, and absolute paths
// outside the repo (and /tmp scratch).
func checkRepoEscape(g *gateText) *core.RefusalError {
	if ref := checkEscapeWords(g); ref != nil {
		return ref
	}
	return checkAbsPaths(g)
}

// checkEscapeWords ports the device, traversal, and home-target arms.
func checkEscapeWords(g *gateText) *core.RefusalError {
	if frag := grepO(reDevProcSys, g.bare); frag != "" {
		return refuse(codeRepoEscape, frag, "/dev|/proc|/sys access escapes the repo")
	}
	if frag := grepO(reDotDot, g.bare); frag != "" {
		return refuse(codeRepoEscape, frag, ".. traversal escapes the repo")
	}
	if frag := grepO(reTilde, g.bare); frag != "" {
		return refuse(codeRepoEscape, frag, "home-directory target escapes the repo")
	}
	if frag := grepO(reDollarHome, g.bare); frag != "" {
		return refuse(codeRepoEscape, frag, "home-directory target escapes the repo")
	}
	return nil
}

// checkAbsPaths ports the absolute-path arm: paths under /tmp or the
// invocation root pass, the rest refuse.
func checkAbsPaths(g *gateText) *core.RefusalError {
	barepaths := rePathPlumb.ReplaceAllString(" "+g.bare, " varpath")
	cands := reAbsPath.FindAllString(barepaths, -1)
	if len(cands) == 0 {
		return nil
	}
	lroot := ""
	if wd, err := os.Getwd(); err == nil {
		lroot = bundle.LowerASCII(wd)
	}
	for _, c := range cands {
		norm, ok := normAbsPath(c, lroot)
		if ok {
			continue
		}
		return refuse(codeRepoEscape, norm,
			"absolute path escapes the repo — use repo-relative paths, /tmp for scratch")
	}
	return nil
}

// normAbsPath strips the separator prefix from an absolute-path candidate
// and reports whether it stays inside the allowed roots (/tmp scratch or
// the invocation root).
func normAbsPath(c, lroot string) (string, bool) {
	if c == "" {
		return "", true
	}
	if !strings.HasPrefix(c, "/") {
		c = c[1:]
	}
	if c == "/tmp" || strings.HasPrefix(c, "/tmp/") {
		return c, true
	}
	return c, lroot != "" && lroot != "/" && (c == lroot || strings.HasPrefix(c, lroot+"/"))
}

// Repo-escape patterns, transliterated from the shell grep arms.
var (
	reDevProcSys = regexp.MustCompile(`/(dev|proc|sys)([^a-z0-9_]|$)`)
	reDotDot     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])\.\.([[:space:];|&(){}"=,/]|$)`)
	reTilde      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,:/])~([[:space:];|&(){}"=,:/]|$|[a-z0-9_.-]+/)`)
	reDollarHome = regexp.MustCompile(`\$home([^a-z0-9_]|$)|\$\{home\}`)
	reAbsPath    = regexp.MustCompile(`(^|[[:space:];|&(){}=,<>])/[^[:space:];|&(){}]*`)
	rePathPlumb  = regexp.MustCompile(` (path|pythonpath|node_path|ld_library_path)=[^[:space:];|&]*`)
)
