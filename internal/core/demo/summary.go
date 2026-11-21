package democore

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/shellquote"
)

// The transport values the envelope and the human render name.
const (
	// fetchedViaGh is the authenticated host read.
	fetchedViaGh = "gh"
	// fetchedViaPublicAPI names the anonymous transport.
	fetchedViaPublicAPI = "public-api"
)

// keptMessage is the warning line that names a kept bundle.
const keptMessage = "demo bundle kept at "

// The two invocation forms, as the envelope's `mode` spells them.
const (
	modeRemote = "remote"
	modeLocal  = "local"
)

// nextStep is the one machine-readable next step every demo envelope carries.
const nextStep = "verdict init --ci github"

const (
	// shortSHALen is the seven-character commit form the CLI prints everywhere.
	shortSHALen = 7
	// milliPerSecond converts a millisecond duration for the human line.
	milliPerSecond = 1000
	// numberBits is the float width the wire's decimal counts are read with.
	numberBits = 64
	// space is the single blank the human fragments are joined with.
	space = " "
	// decimalBase is the base every integer rendering uses.
	decimalBase = 10
)

// Data is the demo envelope's `data` object. The envelope's `data` is free-form
// for a verb that adds no published contract (schemagen models record's wire,
// not this), so the shape stays additive and needs no schema, overlay, or
// conformance pin. Field order is the published order.
type Data struct { //nolint:govet // field order is the published property order
	Mode       string       `json:"mode"`
	URL        string       `json:"url,omitempty"`
	FetchedVia string       `json:"fetched_via"`
	HeadSHA    string       `json:"head_sha"`
	BaseSHA    string       `json:"base_sha"`
	Agent      AgentData    `json:"agent"`
	Finders    int          `json:"finders"`
	ReviewBody string       `json:"review_body"`
	Topology   string       `json:"topology"`
	Findings   FindingsData `json:"findings"`
	Approval   any          `json:"approval"`
	BundleDir  string       `json:"bundle_dir"`
	Kept       bool         `json:"kept"`
	DurationMS int64        `json:"duration_ms"`
}

// AgentData names the agent the demo launched: the row the launcher resolved
// and the binary behind it.
type AgentData struct {
	Name   string `json:"name"`
	Binary string `json:"binary"`
}

// FindingsData is the merge's own count summary: what the review kept, what it
// moved, and what stayed open.
type FindingsData struct {
	Kept  int `json:"kept"`
	Moved int `json:"moved"`
	Open  int `json:"open"`
}

// outcome carries what each composed phase reported, so the summary reads the
// services' own receipts instead of recomputing them.
type outcome struct {
	fetched *fetchcore.Response
	ran     *core.RunResponse
	record  *recordcore.Response
}

// fillResponse assembles the response: the typed envelope data, the exact body
// record rendered, the topology block the human render adds, and the human
// header and next steps.
func fillResponse(resp *Response, req *Request, b *bundle, out outcome, started time.Time) {
	block := topologyBlock(req, out.record.Data.Dir, b.pr)
	resp.Body = out.record.Body
	resp.Header = header(b)
	resp.Steps = Steps(b, out)
	resp.Next = nextStep
	resp.Topology = block
	resp.TopologyAppended = topologyAppended(req, out.record.Data.Dir, b.pr)
	resp.Data = &Data{
		Mode:       mode(b),
		URL:        req.Target,
		FetchedVia: b.fetchedVia,
		HeadSHA:    headSHA(out),
		BaseSHA:    baseSHA(out),
		Agent:      agentData(out),
		Finders:    1,
		ReviewBody: out.record.Body,
		Topology:   block,
		Findings: FindingsData{
			Kept:  out.record.Data.Anchors.Kept,
			Moved: out.record.Data.Anchors.Moved,
			Open:  out.record.Data.Findings.Open,
		},
		Approval:   core.Wire(out.record.Data.Approval),
		BundleDir:  b.dir,
		Kept:       req.Keep,
		DurationMS: elapsed(req, started),
	}
	resp.Facts = summaryLine(resp, out)
	resp.Warnings = dedupe(resp.Warnings)
}

// elapsed renders the run's wall time from the injected clock, never the
// process clock: a scripted run must be able to pin duration_ms, and a clock
// that steps backwards must not print a negative duration.
func elapsed(req *Request, started time.Time) int64 {
	ms := nowOf(req).Sub(started).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

// headSHA reads the reviewed head off the fetch receipt.
func headSHA(out outcome) string {
	if out.fetched == nil || out.fetched.Data == nil {
		return ""
	}
	return out.fetched.Data.HeadSHA
}

// baseSHA reads the reviewed base off the fetch receipt.
func baseSHA(out outcome) string {
	if out.fetched == nil || out.fetched.Data == nil {
		return ""
	}
	return out.fetched.Data.BaseSHA
}

// agentData names the launched agent and its binary from the launcher's
// receipt.
func agentData(out outcome) AgentData {
	if out.ran == nil || out.ran.Data == nil {
		return AgentData{}
	}
	return AgentData{Name: out.ran.Data.Agent, Binary: out.ran.Data.Binary}
}

// mode names the invocation form in the envelope.
func mode(b *bundle) string {
	if b.checkout != "" {
		return modeLocal
	}
	return modeRemote
}

// topologyBlock renders the computed topology for the human output. record
// draws the block only above the diagram size gate (a small PR posts none), so
// the demo has to show it itself for a two-file change: the acceptance
// criterion is that a first impression displays the topology, and the renderer
// already caps the block (60 nodes, 12 000 bytes).
func topologyBlock(req *Request, bundleDir, pr string) string {
	if !req.Effective.StructuralTopology() {
		return ""
	}
	return diagram.Block(diagram.LoadBundle(bundleDir), pr)
}

// topologyAppended reports whether the human render must add the block, i.e.
// whether record's body does not already carry one. The gate reads the same
// override set record's own gate read, so the two can never disagree.
func topologyAppended(req *Request, bundleDir, pr string) bool {
	if topologyBlock(req, bundleDir, pr) == "" {
		return false
	}
	set := overrides.At(filepath.Join(bundleDir, controls.DirName, "overrides"))
	return !diagram.Large(diagram.LoadBundle(bundleDir), set)
}

// header is the one-line identity of the run: what was reviewed and how it was
// read.
func header(b *bundle) string {
	via := "read with gh"
	switch b.fetchedVia {
	case fetchedViaPublicAPI:
		via = "read via the public GitHub API (no gh login)"
	case fetchedViaGit:
		via = "local review of your working tree"
		if b.rangeSpec != "" {
			via = "local review of " + b.rangeSpec
		}
	}
	return fmt.Sprintf("verdict %s · %s#%s · %s", verbName, b.repo, b.pr, via)
}

// summaryLine is the compact facts line under the header: the change's size,
// the commit pair, the agent, and the wall time.
func summaryLine(resp *Response, out outcome) string {
	parts := []string{changeSize(out), commitRange(out)}
	parts = append(parts,
		"agent "+resp.Data.Agent.Name+" (finders "+strconv.Itoa(resp.Data.Finders)+")",
		duration(resp.Data.DurationMS))
	return strings.Join(parts, " · ")
}

// changeSize renders `3 files · +12 −4` from the fetch receipt.
func changeSize(out outcome) string {
	if out.fetched == nil || out.fetched.Data == nil {
		return "0 files"
	}
	d := out.fetched.Data
	return plural(count(d.Files), "file", "files") +
		" · +" + number(d.Additions) + " −" + number(d.Deletions)
}

// commitRange renders `base123 → head456` from the fetch receipt: the range
// reads in review order, the commit reviewed from, then the one under review.
func commitRange(out outcome) string {
	head, base := headSHA(out), baseSHA(out)
	if head == "" && base == "" {
		return "no commit"
	}
	return short(base) + " → " + short(head)
}

// short trims a commit to the seven characters the CLI prints everywhere.
func short(sha string) string {
	if len(sha) > shortSHALen {
		return sha[:shortSHALen]
	}
	if sha == "" {
		return "unknown"
	}
	return sha
}

// count reads a wire count (the fetch data carries any numeric shape).
func count(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		return decimalCount(string(n))
	case jq.Number:
		return decimalCount(string(n))
	default:
		return 0
	}
}

// decimalCount reads one decimal wire number as a whole count; an unreadable
// literal reads zero, which is also what an absent count means.
func decimalCount(raw string) int {
	f, err := strconv.ParseFloat(raw, numberBits)
	if err != nil {
		return 0
	}
	return int(f)
}

// number renders one wire count without a fractional tail.
func number(v any) string { return strconv.Itoa(count(v)) }

// plural renders one count with its word.
func plural(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + space + one
	}
	return strconv.Itoa(n) + space + many
}

// duration renders a wall time for the human line.
func duration(ms int64) string {
	if ms < milliPerSecond {
		return strconv.FormatInt(ms, decimalBase) + "ms"
	}
	return strconv.FormatFloat(float64(ms)/milliPerSecond, 'f', 1, numberBits) + "s"
}

// Steps is the human "next" block: the one-command upgrade path, the other demo
// form, and the explicit three-step equivalent. It never claims anything was
// posted.
func Steps(b *bundle, out outcome) []string {
	agent := agentData(out).Name
	if agent == "" {
		agent = "<agent>"
	}
	explicit := explicitRun(b, agent)
	other := "verdict demo                        # review your own uncommitted changes"
	if b.checkout != "" {
		other = "verdict demo " + publicURLShape
	}
	steps := []string{
		nextStep + "            # review every PR in your repo",
		other,
		explicit,
	}
	if b.fetchedVia == fetchedViaPublicAPI {
		// `verdict fetch` reads with gh, so the printed equivalent does not
		// work under this demo's anonymous setup: name the prerequisite rather
		// than handing back a command that fails at its first call.
		steps = append(steps, "# the demo read the public API anonymously: run 'gh auth login' before the fetch step")
	}
	return steps
}

// explicitRun renders the full equivalent the operator would type instead of
// the demo: fetch the bundle, launch the agent on it, then record the review so
// the same body and receipt are rendered. A local review fetches through
// fetch-local, names its own bundle, and carries the range the demo chose, so
// the re-run reviews the same change; a URL review records the same dry run the
// demo records, because the demo never publishes.
func explicitRun(b *bundle, agent string) string {
	repo := shellquote.Arg(b.repo)
	fetch, name := "verdict fetch "+repo, b.pr
	record := "verdict record " + repo + " " + name + " --dry-run"
	if b.checkout != "" {
		fetch, name = "verdict fetch-local "+repo, localName
		record = "verdict record " + repo + " " + name
	}
	rangeFlag := ""
	if b.rangeSpec != "" {
		rangeFlag = " --range " + shellquote.Arg(b.rangeSpec)
	}
	return fmt.Sprintf("%s %s%s && verdict run %s %s --agent %s --finders 1 && %s",
		fetch, name, rangeFlag, repo, name, shellquote.Arg(agent), record)
}

// dedupe keeps the first occurrence of each warning, preserving order: the same
// line from two composed services is one line to the operator.
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, line := range in {
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}
