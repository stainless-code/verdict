package reportcore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func readFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read "+path, err)
	}
	return raw, nil
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func runLiveProbe(ctx context.Context, st *state) {
	if !st.gate {
		return
	}
	switch {
	case st.flags.Live == "skip":
		st.warn("live recheck skipped (--live skip requested — gating on local dimensions only)")
		st.live.status = liveSkipExplicit
	case isLocalDir(st.dir):
		st.warn("live recheck skipped: local bundle threads unreadable here (gating on local dimensions only)")
		st.live.status = liveSkip
	default:
		probeLiveCount(ctx, st)
	}
}

func isLocalDir(dir string) bool {
	return readcommon.IsLocalBundleDir(dir)
}

func providerName(dir string) string {
	return readcommon.ProviderOf(dir)
}

func probeLiveCount(ctx context.Context, st *state) {
	read, err := fetchcore.ReadLiveThreads(ctx, st.req.ReadRequest, providerName(st.dir), st.headSHA, st.warn)
	writeErrFile(st, st.dir+"/.recheck.err", read.Stderr)
	if err != nil {
		detail := read.Stderr
		if detail == "" {
			detail = err.Error()
		}
		probeUnreachable(st, detail)
		return
	}
	removeErrFile(st.dir + "/.recheck.err")
	if read.Truncated {
		probeUnreachable(st, providerName(st.dir)+" thread read incomplete")
		return
	}
	if read.Malformed > 0 {
		st.warn(fmt.Sprintf("live thread recheck: %d malformed thread node(s) counted as "+
			"unresolved (fail-closed)", read.Malformed))
	}
	st.live.open = read.Open
	if st.live.open > st.nAllOpen {
		st.live.status = liveFail
		return
	}
	st.live.status = livePass
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.req.Runtime; rt != nil {
		commander = rt.Commander
	}
	return commander
}

// probeUnreachable records a live read that did not happen: the gate reads
// neutral under --live auto and fails closed under --live strict.
func probeUnreachable(st *state, detail string) {
	removeErrFile(st.dir + "/.recheck.err")
	detail = firstLine(detail)
	if detail == "" {
		detail = "lookup failed"
	}
	st.live.err = detail
	if st.flags.Live == "strict" {
		st.warn(fmt.Sprintf("live thread recheck unreachable (%s — offline posture is strict, failing closed)", detail))
		st.live.status = liveUnknownStrict
		return
	}
	st.warn(fmt.Sprintf("live thread recheck unreachable (%s — gating on local dimensions only)", detail))
	st.live.status = liveUnknown
}

func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

const errHeadBytes = 120

func writeErrFile(st *state, path, stderr string) {
	if err := atomic.WriteErrSidecar(path, []byte(stderr)); err != nil {
		st.req.Diag(fmt.Sprintf("report: could not stage probe stderr in %s: %v", path, err))
	}
}

func removeErrFile(path string) {
	_ = os.Remove(path)
}

func completeness(ctx context.Context, cmdr core.Commander, dir, headSHA, baseSHA string) (*core.ReportCompleteness, bool, string) {
	var posted string
	if isRegular(dir + "/post.json") {
		posted = readcommon.JSONStr(dir+"/post.json", "head")
	}
	reviewed := headSHA != "" && posted != "" && headSHA == posted
	comp := &core.ReportCompleteness{Reviewed: reviewed}
	if headSHA != "" {
		fetched := headSHA
		comp.FetchedHead = &fetched
	}
	if posted != "" {
		comp.PostedHead = &posted
	}
	completePatchIdentity(ctx, cmdr, dir, headSHA, baseSHA, comp)
	return comp, reviewed, posted
}

// completePatchIdentity fills the same-patch comparison: the reviewed patch
// id from the byte-bound bundle identity, the posted patch id from the
// completed post receipt, and same_patch when both are present and agree.
// Either side unverified leaves its field absent; a legacy bundle with no
// verifiable identity keeps the SHA comparison and the caller warns.
func completePatchIdentity(ctx context.Context, cmdr core.Commander, dir, head, base string, comp *core.ReportCompleteness) {
	reviewed, ok := identity.Verify(ctx, cmdr, dir, head, base)
	if !ok {
		return
	}
	if reviewed.PatchID != nil {
		comp.ReviewedPatchID = reviewed.PatchID
	}
	patch, kind, ok := identity.CompletedPatch(readcommon.PostRecord(dir))
	if !ok {
		return
	}
	comp.PostedPatchID = &patch
	same := identity.Match(reviewed, &identity.Identity{PatchID: &patch, PatchIDKind: kind})
	comp.SamePatch = &same
}

// refetchIncremental marks the hosts whose fetch owns the --base-sha compare:
// a posted head on one of them advances incrementally.
var refetchIncremental = map[string]bool{
	providerGitHub: true,
	providerGitLab: true,
}

func refetchCommand(dir string, req core.ReadRequest, posted string) string {
	prov := ""
	if isRegular(dir + "/PR.json") {
		prov = readcommon.JSONStr(dir+"/PR.json", "provider")
	}
	if isLocalDir(dir) {
		return fmt.Sprintf("verdict fetch-local %s %s", req.Repo, strings.TrimPrefix(req.Pr, "local-"))
	}
	providerFlag := ""
	if prov != "" && prov != providerGitHub {
		providerFlag = " --provider " + prov
	}
	// An absent provider marker reads as fetch's GitHub default, which owns
	// the compare too.
	if posted != "" && (prov == "" || refetchIncremental[prov]) {
		return fmt.Sprintf("verdict fetch %s %s%s --base-sha %s",
			req.Repo, req.Pr, providerFlag, readcommon.ShortSHA(posted))
	}
	return fmt.Sprintf("verdict fetch %s %s%s", req.Repo, req.Pr, providerFlag)
}
