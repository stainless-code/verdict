// Package handoff is the handoff verb's domain service: one read-only
// projection of the open inline findings a fix run would take, each carrying
// the brief text fix prints and the verify command the bundle records, so a
// human or another agent can pick up exactly where fix stops. It writes
// nothing: no ledger, no patch, no store entry.
package handoff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// DefaultFloor is the severity floor handoff applies when the request names
	// none: the floor fix --all takes, so a handoff covers the work fix would.
	DefaultFloor = "important"
	// FloorOff lifts the floor, projecting every open inline finding.
	FloorOff = "off"

	codeNoOpen = "no-open-findings"

	sevUntagged   = "untagged"
	rigorUntagged = "untagged"
)

const (
	msgNoOpen = "handoff: no open inline finding %s — " +
		"lower --min-severity, or read the bundle with report"
	msgNoOpenPaths = "handoff: no open inline finding %s under %s — " +
		"widen --paths or lower --min-severity"
	nextHandoff = "hand one row to a human or another agent, patch a sig with " +
		"verdict fix --sig <sig>, or gate the bundle with report"
)

// Request carries the resolved handoff invocation: the shared read runtime,
// the severity floor, and the portable path globs.
type Request struct {
	core.ReadRequest
	MinSeverity string
	Paths       []string
	Warnings    []string
}

// Response carries the completed projection: its envelope fields, the
// markdown the CLI prints, and the typed data profile MCP and the facade
// return.
type Response struct {
	Data     *Data
	Repo     string
	At       string
	Next     string
	Warnings []string
	Human    []string
}

// Data is the handoff envelope's data profile. Field order is the published
// property order.
type Data struct { //nolint:govet // field order is the published property order
	GeneratedAt string   `format:"date-time"                          json:"generated_at"`
	Bundle      string   `json:"bundle"`
	Floor       string   `enum:"critical|important|informational|off" json:"floor"`
	Paths       []string `json:"paths"`
	Count       int      `json:"count"                                min:"0"`
	Caution     string   `json:"caution"`
	Rows        []Row    `json:"rows"`
}

// Row is one open finding in the projection: where it is, how strongly it is
// evidenced, what to fix, and the brief and verify command fix itself would
// have handed a pass. Field order is the published property order.
type Row struct { //nolint:govet // field order is the published property order
	Sig        string   `json:"sig"`
	Locator    string   `json:"locator"`
	Severity   string   `enum:"critical|important|informational|untagged"   json:"severity"`
	Rigor      string   `enum:"said|pointed|walked|ran|reproduced|untagged" json:"rigor"`
	FixText    string   `json:"fix_text"`
	KBRules    []string `json:"kb_rules"`
	Suggestion *string  `json:"suggestion"`
	BriefText  string   `json:"brief_text"`
	VerifyCmd  *string  `json:"verify_cmd"`
}

// Wire renders one row in published wire order.
func (r *Row) Wire() *jq.Obj {
	return jq.NewObj(
		"sig", r.Sig,
		"locator", r.Locator,
		"severity", r.Severity,
		"rigor", r.Rigor,
		"fix_text", r.FixText,
		"kb_rules", core.WireStrings(r.KBRules),
		"suggestion", r.Suggestion,
		"brief_text", r.BriefText,
		"verify_cmd", r.VerifyCmd,
	)
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	rows := make([]any, 0, len(d.Rows))
	for i := range d.Rows {
		rows = append(rows, d.Rows[i].Wire())
	}
	return jq.NewObj(
		"generated_at", d.GeneratedAt,
		"bundle", d.Bundle,
		"floor", d.Floor,
		"paths", core.WireStrings(d.Paths),
		"count", d.Count,
		"caution", d.Caution,
		"rows", rows,
	)
}

// Run projects one bundle's open findings. It selects through the same
// readcommon.Selector fix --all uses, so the two verbs cannot pick different
// work, and it refuses rather than printing an empty handoff.
func Run(_ context.Context, req *Request) (*Response, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, ref
	}
	floor, ref := floorOf(req.MinSeverity)
	if ref != nil {
		return nil, ref
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return nil, refusalOf(err)
	}
	picks := readcommon.Select(entries, &readcommon.Selector{
		Status: readcommon.StatusOpen,
		Kind:   readcommon.KindInline,
		Floor:  floor,
		Paths:  req.Paths,
	})
	if len(picks) == 0 {
		return nil, &core.RefusalError{Code: codeNoOpen, Message: emptyMsg(floor, req.Paths)}
	}
	now := req.TimeUTC()
	rows := make([]Row, 0, len(picks))
	for _, pick := range picks {
		rows = append(rows, rowOf(pick.Entry, dir))
	}
	data := &Data{
		GeneratedAt: now,
		Bundle:      dir,
		Floor:       floor,
		Paths:       append([]string{}, req.Paths...),
		Count:       len(rows),
		Caution:     core.FindingCaution,
		Rows:        rows,
	}
	return &Response{
		Data:     data,
		Warnings: req.Warnings,
		Human:    render(data),
		Repo:     req.Repo,
		At:       now,
		Next:     nextHandoff,
	}, nil
}

func floorOf(floor string) (string, *core.RefusalError) {
	if floor == "" {
		return DefaultFloor, nil
	}
	if floor == FloorOff || config.SeverityRank(floor) > 0 {
		return floor, nil
	}
	return "", &core.RefusalError{
		Code: "bad-min-severity",
		Message: "handoff: --min-severity takes " +
			"critical|important|informational|off",
	}
}

func refusalOf(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: "corrupt-store", Message: err.Error()}
}

func emptyMsg(floor string, paths []string) string {
	above := "at or above " + floor
	if floor == FloorOff {
		above = "with no severity floor"
	}
	if len(paths) > 0 {
		return fmt.Sprintf(msgNoOpenPaths, above, strings.Join(paths, ", "))
	}
	return fmt.Sprintf(msgNoOpen, above)
}

func rowOf(entry any, dir string) Row {
	sig := jq.Interp(readcommon.FieldOf(entry, "sig"))
	row := Row{
		Sig:       sig,
		Locator:   readcommon.Locator(entry),
		Severity:  severityOf(entry),
		Rigor:     rigorOf(entry),
		FixText:   jq.Interp(readcommon.FieldOf(entry, "body")),
		KBRules:   kbRulesOf(entry),
		BriefText: brief.Render(brief.FromRecord(recOf(entry))),
	}
	if hint := readcommon.VerifyHint(dir, sig); hint != nil {
		command := hint.VerifyCmd
		row.VerifyCmd = &command
	}
	if suggestion, ok := readcommon.FieldOf(entry, "suggestion").(string); ok {
		row.Suggestion = &suggestion
	}
	return row
}

func recOf(entry any) *jq.Obj {
	rec, ok := entry.(*jq.Obj)
	if !ok {
		return nil
	}
	return rec
}

func severityOf(entry any) string {
	switch severity := jq.Interp(readcommon.FieldOf(entry, "severity")); severity {
	case "critical", "important", "informational":
		return severity
	default:
		return sevUntagged
	}
}

func rigorOf(entry any) string {
	rigor, ok := readcommon.FieldOf(entry, "rigor").(string)
	if !ok {
		return rigorUntagged
	}
	switch rigor {
	case "said", "pointed", "walked", "ran", "reproduced":
		return rigor
	default:
		return rigorUntagged
	}
}

func kbRulesOf(entry any) []string {
	items, ok := readcommon.FieldOf(entry, "kb").([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if id, ok := item.(string); ok {
			out = append(out, id)
		}
	}
	return out
}
