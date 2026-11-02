package verifycore

import (
	"regexp"

	"github.com/stainless-code/verdict/internal/core"
)

// checkNetworked ports the verify-networked arms in shell order.
func checkNetworked(g *gateText) *core.RefusalError {
	if ref := checkPipeToShell(g); ref != nil {
		return ref
	}
	if ref := checkFetchClients(g); ref != nil {
		return ref
	}
	if ref := checkRemoteInstall(g); ref != nil {
		return ref
	}
	if ref := checkContainersNet(g); ref != nil {
		return ref
	}
	return checkSubstFetch(g)
}

// checkPipeToShell ports the pipe-to-shell, coproc, parallel, curl
// download, upload-flag, scheme, and wget arms.
func checkPipeToShell(g *gateText) *core.RefusalError {
	if ref := checkFetchPipe(g); ref != nil {
		return ref
	}
	if ref := checkCoprocParallel(g); ref != nil {
		return ref
	}
	if ref := checkCurlShapes(g); ref != nil {
		return ref
	}
	return checkWgetShapes(g)
}

// checkFetchPipe ports the pipe-to-shell and output-procsub arms.
func checkFetchPipe(g *gateText) *core.RefusalError {
	if !reFetchWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(rePipeToShell, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "pipe-to-shell executes a network fetch")
	}
	if frag := grepO(reProcSubstShell, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"output process substitution into a shell executes a network fetch")
	}
	return nil
}

// checkCoprocParallel ports the coproc and parallel-driver arms.
func checkCoprocParallel(g *gateText) *core.RefusalError {
	if frag := grepO(reCoproc, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "coproc spawns a background shell — fetch-execution shape")
	}
	if !reParallelWord.MatchString(g.bare) {
		return nil
	}
	pseg := grepO(reParallelSeg, g.bare)
	if reFetchCurlWget.MatchString(g.bare) || reShellInSeg.MatchString(pseg) {
		return refuse(codeNetworked, pseg, "parallel driving a shell or a fetch tool executes remote content")
	}
	return nil
}

// checkCurlShapes ports the curl download, upload-flag, and scheme arms.
func checkCurlShapes(g *gateText) *core.RefusalError {
	if frag := grepO(reCurlDownload, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "curl exfil/download-to-file shape — fetch fixtures first, run offline")
	}
	if frag := grepO(reCurlUpload, g.csbare); frag != "" {
		return refuse(codeNetworked, frag, "curl upload shape — fetch fixtures first, run offline")
	}
	if frag := grepO(reFetchScheme, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "non-http fetch scheme — fetch fixtures first, run offline")
	}
	return nil
}

// checkWgetShapes ports the wget and transfer-client arms.
func checkWgetShapes(g *gateText) *core.RefusalError {
	if frag := grepO(reWgetPost, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "wget download/post shape — fetch fixtures first, run offline")
	}
	if frag := grepO(reWgetUpperO, g.csbare); frag != "" {
		return refuse(codeNetworked, frag, "wget -O download-to-file shape — fetch fixtures first, run offline")
	}
	if frag := grepO(reTransferClient, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "interactive file-transfer client — fetch fixtures first, run offline")
	}
	return nil
}

// checkFetchClients ports the gh-write, openssl, and ping arms.
func checkFetchClients(g *gateText) *core.RefusalError {
	if ref := checkGhWrite(g); ref != nil {
		return ref
	}
	if frag := grepO(reOpensslSClient, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "openssl s_client is a TLS exfil oracle — fetch fixtures first, run offline")
	}
	if frag := grepO(rePingPattern, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "ping -p sends a pattern payload — probe without -p")
	}
	return nil
}

// checkGhWrite ports the gh upload and write-method arms.
func checkGhWrite(g *gateText) *core.RefusalError {
	if !reGhWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reGhGistCreate, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gh gist create uploads file content — paste fixtures, do not upload")
	}
	if frag := grepO(reGhGistEdit, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gh gist edit uploads file content — paste fixtures, do not upload")
	}
	if frag := grepO(reGhReleaseUpload, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gh release upload ships artifacts — paste fixtures, do not upload")
	}
	if frag := grepO(reGhAPIMethod, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gh api with a write method sends a body — use GET reads only")
	}
	if frag := grepO(reGhAPIInput, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gh api --input sends a body — use GET reads only")
	}
	return checkGhAPIFile(g)
}

// checkGhAPIFile ports the gh api file-inclusion arm.
func checkGhAPIFile(g *gateText) *core.RefusalError {
	if !reGhAPISeg.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reGhAPIFile, g.csbare); frag != "" {
		return refuse(codeNetworked, frag, "gh api file-inclusion flag sends file bytes — use GET reads only")
	}
	return nil
}

// checkRemoteInstall ports the URL-install, runner, and redirector arms.
func checkRemoteInstall(g *gateText) *core.RefusalError {
	if frag := grepO(rePkgURLInstall, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"package install from a URL executes remote code "+
				"at install time — vendor the fixture, install offline")
	}
	if frag := grepO(reNpxPackage, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"npx -p/--package fetches and executes a remote package — vendor the fixture, install offline")
	}
	if frag := grepO(reDlx, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "dlx/bunx fetches and executes a remote package — vendor the fixture, install offline")
	}
	if frag := grepO(reCargoGit, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"cargo install --git fetches and executes remote code — vendor the fixture, install offline")
	}
	if frag := grepO(reGemSource, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "gem install --source fetches a remote package — vendor the fixture, install offline")
	}
	if frag := grepO(reGoRemoteInstall, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"go install/get of a remote module fetches and "+
				"executes remote code — vendor the fixture, install offline")
	}
	if frag := grepO(reIndexRedirector, g.bare); frag != "" {
		return refuse(codeNetworked, frag,
			"package-index redirector env sends installs to an evil "+
				"index — install from vendored fixtures without redirector env")
	}
	return nil
}

// checkContainersNet ports the container-image, netcat, ssh, and rsync arms.
func checkContainersNet(g *gateText) *core.RefusalError {
	if ref := checkContainerShapes(g); ref != nil {
		return ref
	}
	return checkRsyncNetcat(g)
}

// checkContainerShapes ports the container pull/build/run arms.
func checkContainerShapes(g *gateText) *core.RefusalError {
	if frag := grepO(reContainerPull, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "container image pull/push fetches remote code — vendor the fixture, run offline")
	}
	if frag := grepO(reContainerBuild, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "container build from a URL context fetches remote code — build from a local context")
	}
	if frag := grepO(reContainerRunPull, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "container run --pull fetches a remote image — run a local image without --pull")
	}
	if frag := grepO(reContainerRunImage, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "container run of a registry-path/tag image fetches remote code — run a local image")
	}
	return nil
}

// checkRsyncNetcat ports the netcat, ssh, and rsync arms.
func checkRsyncNetcat(g *gateText) *core.RefusalError {
	if frag := grepO(reNetcat, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "netcat connect/listen shape")
	}
	if frag := grepO(reSSHOutbound, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "ssh outbound shape")
	}
	if !reRsyncWord.MatchString(g.bare) {
		return nil
	}
	if frag := grepO(reRsyncShell, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "rsync -e/--rsh runs the command locally — copy fixtures without -e/--rsh")
	}
	if frag := grepO(reRsyncRemote, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "rsync to/from a remote host — copy fixtures locally")
	}
	return nil
}

// checkSubstFetch ports the substitution-over-fetch, process-substitution,
// and xargs-shell arms.
func checkSubstFetch(g *gateText) *core.RefusalError {
	if frag := grepO(reSubstFetch, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "command substitution over a network fetch")
	}
	if frag := grepO(reSubstFetchBt, g.scan); frag != "" {
		return refuse(codeNetworked, frag, "command substitution over a network fetch")
	}
	if frag := grepO(reProcSubstFetch, g.bare); frag != "" {
		return refuse(codeNetworked, frag, "process substitution over a network fetch executes it — fetch fixtures first, run offline")
	}
	if reXargsWord.MatchString(g.bare) {
		if frag := grepO(reXargsShell, g.bare); frag != "" {
			return refuse(codeNetworked, frag, "piping into a shell via xargs executes a network fetch")
		}
	}
	return nil
}

// Networked-class patterns, transliterated from the shell grep arms.
var (
	rePipeToShell = regexp.MustCompile(`\|[[:space:]]*(\{|\()?([[:space:]]*)(sudo[[:space:]]+)?((busybox|command|` +
		`env|nice|nohup|stdbuf|timeout|exec)([[:space:]]+[^[:space:];|&(){}]+)*[[:space:]]+)?` +
		`([^[:space:];|&]*/)?(sh|bash|zsh|fish|dash)([[:space:];|&)}]|$)`)
	reProcSubstShell = regexp.MustCompile(`>\([[:space:]]*(sudo[[:space:]]+)?((busybox|command|env|nice|` +
		`nohup|stdbuf|timeout|exec)([[:space:]]+[^[:space:];|&(){}]+)*[[:space:]]+)?` +
		`([^[:space:];|&]*/)?(sh|bash|zsh|fish|dash)([[:space:];|&)}]|$)`)
	reCoproc        = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])coproc([[:space:];|&]|$)`)
	reParallelWord  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])parallel([[:space:];|&]|$)`)
	reParallelSeg   = regexp.MustCompile(`parallel[^;|&]*`)
	reFetchCurlWget = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(curl|wget)([[:space:];|&]|$)`)
	reShellInSeg    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(sh|bash|zsh|fish|dash)([[:space:];|&]|$)`)
	reCurlDownload  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])curl[[:space:]]+([^;|&]*[[:space:]])?(-X[[:space:]]*(post|` +
		`put)|--output|-o[[:space:]]|-O([[:space:];|&]|$))`)
	reCurlUpload = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])[Cc][Uu][Rr][Ll][[:space:]]+([^;|&]*[[:space:]])?((--data)([^a-zA-Z]|` +
		`$)|-[a-z]*[dTF]|--upload-file([^a-zA-Z]|$)|--form(-string)?([^a-zA-Z-]|$))`)
	reFetchScheme = regexp.MustCompile(`(file|ftp|ftps|git|dict|gopher|tftp|ldap|ldaps|sftp|scp|rsync)://`)
	reWgetPost    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])wget[[:space:]]+[^;|` +
		`&]*--(output-document|post-data|post-file)([^a-z-]|$)`)
	reWgetUpperO      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])[Ww][Gg][Ee][Tt][[:space:]]+[^;|&]*-O([^a-z0-9-]|$)`)
	reTransferClient  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(ftp|tftp|lftp)([[:space:];|&]|$)`)
	reGhWord          = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh([[:space:];|&]|$)`)
	reGhGistCreate    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*gist[[:space:]]+create([^a-z]|$)`)
	reGhGistEdit      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*gist[[:space:]]+edit([^a-z]|$)`)
	reGhReleaseUpload = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*release[[:space:]]+upload([^a-z]|$)`)
	reGhAPIMethod     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*api[[:space:]]+[^;|` +
		`&]*--method([[:space:]]*=|[[:space:]]+)(post|put|patch|delete)([^a-z]|$)`)
	reGhAPIInput     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*api[[:space:]]+[^;|&]*--input([^a-z-]|$)`)
	reGhAPISeg       = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gh[[:space:]]+[^;|&]*api([[:space:];|&]|$)`)
	reGhAPIFile      = regexp.MustCompile(`(-F|--field|--raw-field)[^;|&]*=@`)
	reOpensslSClient = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])openssl[[:space:]]+[^;|&]*s_client([^a-z_]|$)`)
	rePingPattern    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])ping[[:space:]]+[^;|&]*-[a-z]*p([^a-z0-9-]|$)`)
	rePkgURLInstall  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(pip|pip3|npm|yarn|pnpm|bun|cargo|gem|` +
		`go)[[:space:]]+[^;|&]*(install|add)[[:space:]]+[^;|&]*https?://`)
	reNpxPackage      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])npx[[:space:]]+[^;|&]*(-p([^a-z0-9-]|$)|--package)`)
	reDlx             = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(yarn[[:space:]]+dlx|pnpm[[:space:]]+dlx|bunx)([^a-z0-9-]|$)`)
	reCargoGit        = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])cargo[[:space:]]+[^;|&]*--git([^a-z-]|$)`)
	reGemSource       = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])gem[[:space:]]+[^;|&]*--source([^a-z-]|$)`)
	reGoRemoteInstall = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])go[[:space:]]+[^;|&]*(get|` +
		`install)[[:space:]]+[a-z0-9_.-]*\.[a-z][a-z]+/`)
	reIndexRedirector = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(pip_index_url|pip_find_links|pip_extra_index_url|` +
		`pip_trusted_host|pip_config_file|npm_config_registry)[[:space:]]*=`)
	reContainerPull     = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(docker|podman|nerdctl)[[:space:]]+(pull|push)([^a-z]|$)`)
	reContainerBuild    = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(docker|podman|nerdctl)[[:space:]]+build[^;|&]*https?://`)
	reContainerRunPull  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(docker|podman|nerdctl)[[:space:]]+run[^;|&]*--pull([^a-z0-9-]|$)`)
	reContainerRunImage = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(docker|podman|nerdctl)[[:space:]]+run(` +
		`[[:space:]]+(-(v|e|w|u|p)[[:space:]]+[^[:space:];|&]+|--(volume|env|` +
		`workdir|user|publish|name|network|` +
		`entrypoint|mount|label)(=([^[:space:];|&]+)?|[[:space:]]+[^[:space:];|&]+)|` +
		`-[a-z]+|--[a-z-]+))*[[:space:]]+[^[:space:];|&/=-][^[:space:];|&=]*[./:][^[:space:];|&=]*`)
	reNetcat      = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(nc|ncat|netcat|socat)([[:space:];|&]|$)`)
	reSSHOutbound = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])(ssh|scp|sftp)([[:space:];|&]|$)`)
	reRsyncWord   = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])rsync([[:space:];|&]|$)`)
	reRsyncShell  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])rsync[[:space:]]+([^;|` +
		`&]*[[:space:]])?(-[a-z]*e[a-z]*|--rsh)([^a-z0-9-]|$)`)
	reRsyncRemote = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])rsync[[:space:]]+[^;|` +
		`&]*([a-z0-9_.-]*@|::|[[:space:]][a-z0-9_.-]+:[^[:space:];|&])`)
	reSubstFetch = regexp.MustCompile(`\$\(([^[:space:];|&()]*\/)?(curl|wget|ftp|tftp|lftp|` +
		`ncat|netcat|socat|ssh|scp|nc)([[:space:];|&)]|$)`)
	reSubstFetchBt   = regexp.MustCompile("`([^[:space:];|&]*/)?(curl|wget|ftp|tftp|lftp|ncat|netcat|socat|ssh|scp|nc)([[:space:];|&]|`|$)")
	reProcSubstFetch = regexp.MustCompile(`<\([[:space:]]*([^[:space:];|&()]*\/)?(curl|wget|ftp|tftp|` +
		`lftp|ncat|netcat|socat|ssh|scp|nc)([[:space:];|&)]|$)`)
	reXargsWord  = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])xargs([[:space:];|&]|$)`)
	reXargsShell = regexp.MustCompile(`(^|[[:space:];|&(){}"=,/])xargs[^;|&]*[[:space:];|&/](sh|bash|zsh|fish|dash)([[:space:];|&)]|$)`)
)
