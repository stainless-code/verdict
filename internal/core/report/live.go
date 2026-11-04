package reportcore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
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
		prov := providerName(st.dir)
		if prov != "" && prov != "github" {
			st.warn(fmt.Sprintf("live recheck skipped: %s threads unreadable here (gating on local dimensions only)", prov))
			st.live.status = liveSkip
			return
		}
		probeLiveCount(ctx, st)
	}
}

func isLocalDir(dir string) bool {
	return readcommon.IsLocalBundle(dir)
}

func providerName(dir string) string {
	return readcommon.ProviderOf(dir)
}

func probeLiveCount(ctx context.Context, st *state) {
	owner, name := readcommon.SplitOwner(st.req.Repo)
	nodes, res, err := readcommon.Threads(ctx, st.commander(), owner, name, st.req.Pr, false)
	writeErrFile(st, st.dir+"/.recheck.err", res.Stderr)
	if err != nil {
		probeUnreachable(st, res.Stderr)
		return
	}
	removeErrFile(st.dir + "/.recheck.err")
	if malformed := readcommon.MalformedThreadCount(nodes); malformed > 0 {
		st.warn(fmt.Sprintf("live thread recheck: %d malformed thread node(s) counted as "+
			"unresolved (fail-closed)", malformed))
	}
	st.live.open = readcommon.OpenThreadCount(nodes)
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

func probeUnreachable(st *state, stderr string) {
	removeErrFile(st.dir + "/.recheck.err")
	detail := firstLine(stderr)
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

func completeness(dir, headSHA string) (*core.ReportCompleteness, bool, string) {
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
	return comp, reviewed, posted
}

func refetchCommand(dir string, req core.ReadRequest, posted string) string {
	prov := ""
	if isRegular(dir + "/PR.json") {
		prov = readcommon.JSONStr(dir+"/PR.json", "provider")
	}
	if isLocalDir(dir) {
		return fmt.Sprintf("verdict fetch-local %s %s", req.Repo, strings.TrimPrefix(req.Pr, "local-"))
	}
	if prov != "" && prov != "github" {
		return fmt.Sprintf("verdict fetch %s %s --provider %s", req.Repo, req.Pr, prov)
	}
	if posted != "" {
		return fmt.Sprintf("verdict fetch %s %s --base-sha %s", req.Repo, req.Pr, readcommon.ShortSHA(posted))
	}
	return fmt.Sprintf("verdict fetch %s %s", req.Repo, req.Pr)
}
