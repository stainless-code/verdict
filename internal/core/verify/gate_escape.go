package verifycore

import (
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func checkRepoEscape(g *gateText) *core.RefusalError {
	if ref := checkEscapeWords(g); ref != nil {
		return ref
	}
	return checkAbsPaths(g)
}

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

func checkAbsPaths(g *gateText) *core.RefusalError {
	barepaths := rePathPlumb.ReplaceAllString(" "+g.bare, " varpath")
	cands := reAbsPath.FindAllString(barepaths, -1)
	if len(cands) == 0 {
		return nil
	}
	lroot := ""
	if wd, err := os.Getwd(); err == nil {
		lroot = readcommon.ASCIILower(wd)
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

var (
	reDevProcSys = regexp.MustCompile(`/(dev|proc|sys)([^a-z0-9_]|$)`)
	reDotDot     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])\.\.([[:space:];|&(){}"=,/]|$)`)
	reTilde      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,:/])~([[:space:];|&(){}"=,:/]|$|[a-z0-9_.-]+/)`)
	reDollarHome = regexp.MustCompile(`\$home([^a-z0-9_]|$)|\$\{home\}`)
	reAbsPath    = regexp.MustCompile(`(^|[[:space:];|&(){}=,<>])/[^[:space:];|&(){}]*`)
	rePathPlumb  = regexp.MustCompile(` (path|pythonpath|node_path|ld_library_path)=[^[:space:];|&]*`)
)
