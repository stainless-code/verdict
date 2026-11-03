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

// readFile reads a whole file.
func readFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read "+path, err)
	}
	return raw, nil
}

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// jsonField reads one string field with || true semantics ("" when the
// file is missing, invalid, or the field is absent).
func jsonField(path, field string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(m[field])
}

// runLiveProbe ports the live-thread probe: the network-backed first half
// of the gate's third dimension. Advisory runs (no gate flag) skip it with
// zero network; --live skip warns and stays neutral; structural skips
// (local bundles, non-GitHub providers) warn neutral; unreachable lookups
// warn neutral under auto and fail closed under strict.
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

// isLocalDir reports whether dir is a local bundle.
func isLocalDir(dir string) bool {
	return strings.HasPrefix(dir, ".review/local-")
}

// providerName reads the bundle provider ("" when the record is missing).
func providerName(dir string) string {
	return readcommon.ProviderOf(dir)
}

// probeLiveCount runs the live count lookup and maps the outcome.
func probeLiveCount(ctx context.Context, st *state) {
	n, res, err := readcommon.LiveOpenThreadCount(ctx, st.req.Repo, st.req.Pr)
	writeErrFile(st, st.dir+"/.recheck.err", res.Stderr)
	if err != nil {
		probeUnreachable(st, res.Stderr)
		return
	}
	removeErrFile(st.dir + "/.recheck.err")
	if n < 0 {
		n = 0
	}
	st.live.open = n
	if n > st.nAllOpen {
		st.live.status = liveFail
		return
	}
	st.live.status = livePass
}

// probeUnreachable maps a failed lookup to the live posture.
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

// firstLine ports `head -c 120 | tr '\n' ' '`: the first 120 bytes with
// newlines flattened to spaces.
func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// errHeadBytes caps the probe error detail.
const errHeadBytes = 120

// writeErrFile stages owner-only probe stderr beside the bundle through the
// shared atomic path (removed after the probe either way, so readers stay
// side-effect free). The write is best-effort like the shell's 2>
// redirect, so a failure warns instead of refusing: the file is a debug
// side channel and the run must not fail over garnish.
func writeErrFile(st *state, path, stderr string) {
	if err := atomic.WriteErrSidecar(path, []byte(stderr)); err != nil {
		// Un-actionable beyond the notice: the live data already loaded.
		st.req.Diag(fmt.Sprintf("report: could not stage probe stderr in %s: %v", path, err))
	}
}

// removeErrFile removes a staged probe file.
func removeErrFile(path string) {
	_ = os.Remove(path)
}

// completeness ports the HEAD-reviewed dimension: pass iff a completed post
// record exists, parses, and carries the fetched HEAD. Stays nil without
// the flag.
func completeness(dir, headSHA string, _ core.ReadRequest) (*core.ReportCompleteness, bool, string) {
	var posted string
	if isRegular(dir + "/post.json") {
		posted = jsonField(dir+"/post.json", "head")
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

// refetchCommand ports the re-fetch recovery command for the gate tail.
func refetchCommand(dir string, req core.ReadRequest, posted string) string {
	prov := ""
	if isRegular(dir + "/PR.json") {
		prov = jsonField(dir+"/PR.json", "provider")
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

// fieldOf reads one entry field (nil-safe for null entries).
func fieldOf(entry any, key string) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}
