package verifycore

import (
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func checkRepoEscape(g *gateText) *core.RefusalError {
	if ref := checkEscapeWords(g.bare); ref != nil {
		return ref
	}
	esc := detachOptionValues(g.bare)
	if esc == g.bare {
		return checkAbsPaths(g.bare, false)
	}
	if ref := checkAbsPaths(g.bare, false); ref != nil {
		return ref
	}
	if ref := checkEscapeWords(esc); ref != nil {
		return ref
	}
	return checkAbsPaths(esc, true)
}

// detachOptionValues splits a short option from the value getopt attaches to
// it, so -C/etc, -czf/etc/x, -C../x, and -C~/x reach the escape arms as the
// path they are rather than as text glued to a flag.
func detachOptionValues(bare string) string {
	return reAttachedValue.ReplaceAllString(bare, "$1$2 $3")
}

func checkEscapeWords(bare string) *core.RefusalError {
	if frag := grepO(reDevProcSys, bare); frag != "" {
		return refuse(codeRepoEscape, frag, "/dev|/proc|/sys access escapes the repo")
	}
	if frag := grepO(reDotDot, bare); frag != "" {
		return refuse(codeRepoEscape, frag, ".. traversal escapes the repo")
	}
	if frag := grepO(reTilde, bare); frag != "" {
		return refuse(codeRepoEscape, frag, "home-directory target escapes the repo")
	}
	if frag := grepO(reDollarHome, bare); frag != "" {
		return refuse(codeRepoEscape, frag, "home-directory target escapes the repo")
	}
	return nil
}

func checkAbsPaths(bare string, attached bool) *core.RefusalError {
	barepaths := rePathPlumb.ReplaceAllString(" "+bare, " varpath")
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
		detail := "absolute path escapes the repo — use repo-relative paths, /tmp for scratch"
		if attached {
			detail = "absolute path attaches to an option — separate the value, and use repo-relative paths or /tmp for scratch"
		}
		return refuse(codeRepoEscape, norm, detail)
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
	// getopt glues a short option to its value (-C/etc, -czf/etc/x, -C../x,
	// -C~/x): the flag run is letters or digits, the value starts with /, ..,
	// or ~, and tar's positional -C takes the same shape after --.
	reAttachedValue = regexp.MustCompile(
		`(^|[[:space:];|&(){}])(-[[:alpha:][:digit:]]+)(/[^[:space:];|&(){}]*` +
			`|\.\.(/[^[:space:];|&(){}]*)?|~(/[^[:space:];|&(){}]*)?)`)
)
