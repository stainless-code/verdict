package verifycore

import (
	"regexp"

	"github.com/stainless-code/verdict/internal/core"
)

func checkCredential(g *gateText) *core.RefusalError {
	if ref := checkCredPath(g); ref != nil {
		return ref
	}
	if ref := checkEnvPipe(g); ref != nil {
		return ref
	}
	if ref := checkFetchSubst(g); ref != nil {
		return ref
	}
	return checkDNSSubst(g)
}

func checkCredPath(g *gateText) *core.RefusalError {
	if frag := grepO(reCredPath, g.bare); frag != "" {
		return refuse(codeCredential, frag, "credential-path read")
	}
	return nil
}

func checkEnvPipe(g *gateText) *core.RefusalError {
	if !reEnvWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reEnvPipeNet, g.bare); frag != "" {
		return refuse(codeCredential, frag, "env piped into the network exfils credentials")
	}
	return nil
}

func checkFetchSubst(g *gateText) *core.RefusalError {
	if !reFetchWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reEnvSubst, g.bare); frag != "" {
		return refuse(codeCredential, frag, "env output embedded in a networked command exfils credentials")
	}
	if frag := grepO(reEnvSubstBt, g.scan); frag != "" {
		return refuse(codeCredential, frag, "env output embedded in a networked command exfils credentials")
	}
	return checkReconSubst(g)
}

func checkReconSubst(g *gateText) *core.RefusalError {
	if frag := grepO(reReconSubst, g.bare); frag != "" {
		return refuse(codeCredential, frag, "identity/recon output embedded in a networked command exfils credentials")
	}
	if frag := grepO(reReconSubstBt, g.scan); frag != "" {
		return refuse(codeCredential, frag, "identity/recon output embedded in a networked command exfils credentials")
	}
	if frag := grepO(reIdentityVar, g.bare); frag != "" {
		return refuse(codeCredential, frag,
			"identity variable embedded in a networked command exfils through the request — fetch fixtures first, run offline")
	}
	if frag := grepO(reIdentityVarBrace, g.bare); frag != "" {
		return refuse(codeCredential, frag,
			"identity variable embedded in a networked command exfils through the request — fetch fixtures first, run offline")
	}
	return nil
}

func checkDNSSubst(g *gateText) *core.RefusalError {
	if frag := grepO(reDNSSubst, g.bare); frag != "" {
		return refuse(codeCredential, frag, "command substitution in a DNS query exfils through resolver labels")
	}
	if frag := grepO(reDNSSubstBt, g.scan); frag != "" {
		return refuse(codeCredential, frag, "command substitution in a DNS query exfils through resolver labels")
	}
	return nil
}

var (
	reCredPath   = regexp.MustCompile(`/\.(ssh|aws|gnupg|pki)|\.config/gh|/\.netrc|\.docker/config\.json`)
	reEnvWord    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(env|printenv)([[:space:];|&]|$)`)
	reEnvPipeNet = regexp.MustCompile(`\|[[:space:]]*([^[:space:];|&]*/)?(curl|wget|ncat|netcat|socat|ssh|nc)([[:space:];|&]|$)`)
	reFetchWord  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(curl|wget|ftp|tftp|lftp|ncat|netcat|socat|ssh|scp)([[:space:];|&]|$)`)
	reEnvSubst   = regexp.MustCompile(`\$\((env|printenv)([[:space:];|&)]|$)`)
	reEnvSubstBt = regexp.MustCompile("`(env|printenv)([[:space:];|&]|$)")
	reReconSubst = regexp.MustCompile(`\$\(([^()]*[^a-z_])?(whoami|id|uname|hostname|env|printenv|` +
		`groups|who|users|logname|getent|sw_vers)([^a-z_]|$)`)
	reReconSubstBt = regexp.MustCompile("`(([^`]*[^a-z_])?(whoami|id|uname|hostname|env|printenv|" +
		"groups|who|users|logname|getent|sw_vers)([^a-z_]|$)[^`]*)`")
	reIdentityVar      = regexp.MustCompile(`\$(user|logname|hostname|uid|euid|groups)([^a-z0-9_]|$)`)
	reIdentityVarBrace = regexp.MustCompile(`\$\{(user|logname|hostname|uid|euid|groups)([^a-z0-9_]|$)`)
	reDNSSubst         = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(dig|nslookup|host)[[:space:]]+[^;|&]*\$\(`)
	reDNSSubstBt       = regexp.MustCompile("(^|[[:space:];|&(){}\"=,/])(dig|nslookup|host)[[:space:]]+[^;|&]*`")
)
