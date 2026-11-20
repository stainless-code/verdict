package run

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

type unionCandidate struct {
	row       *jq.Obj
	signature string
	origins   []any
	verified  bool
}

const (
	maxUnionFindings = 100
	unionSeverity    = "severity"
)

func discoveryUnion(execution string, diff []byte, reviews []*jq.Obj) (*jq.Obj, *jq.Obj, error) {
	candidates := make(map[string]*unionCandidate)
	idx := evidence.IndexDiff(diff)
	receipts, specs := []any{}, []any{}
	receiptKeys := make(map[string]bool)
	specKeys := make(map[string]bool)
	for i, review := range reviews {
		if review == nil {
			continue
		}
		finder := fmt.Sprintf("finder-%d", i+1)
		rows, _ := review.Get("findings")
		if err := mergeDiscovery(candidates, idx, finder, jq.Elements(rows)); err != nil {
			return nil, nil, err
		}

		receipts = appendUnionRows(receipts, receiptKeys, review, "receipts")
		specs = appendUnionRows(specs, specKeys, review, "spec_checks")
	}
	if len(candidates) > maxUnionFindings {
		return nil, nil, jq.NewErrf("union exceeds 100 findings")
	}
	return renderUnion(execution, candidates, receipts, specs)
}

func mergeDiscovery(candidates map[string]*unionCandidate, idx *evidence.DiffIndex, finder string, rows []any) error {
	for n, value := range rows {
		row, ok := value.(*jq.Obj)
		if !ok {
			return jq.NewErrf("%s finding %d is not an object", finder, n)
		}
		signature := sig.Finding(jq.StrOf(readcommon.FieldOf(row, "path")), "", jq.StrOf(readcommon.FieldOf(row, "claim")))
		line, _ := jq.IntOf(readcommon.FieldOf(row, "line"))
		verified := evidence.MatchQuote(idx, evidence.QuoteRequest{
			Path: jq.StrOf(readcommon.FieldOf(row, "path")), Side: quoteSide(row),
			Line: line, Quote: jq.StrOf(readcommon.FieldOf(row, "quote")),
		}).Verified()
		origin := jq.NewObj("finder", finder, "index", n, "finding", row)
		prior := candidates[signature]
		if prior == nil {
			candidates[signature] = &unionCandidate{row: row, origins: []any{origin}, signature: signature, verified: verified}
			continue
		}
		mergeDuplicate(prior, row, origin, verified)
	}
	return nil
}

func renderUnion(execution string, candidates map[string]*unionCandidate, receipts, specs []any) (*jq.Obj, *jq.Obj, error) {
	ordered := make([]*unionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	slices.SortFunc(ordered, func(a, b *unionCandidate) int {
		rank := core.TierRank(readcommon.FieldOf(a.row, unionSeverity)) - core.TierRank(readcommon.FieldOf(b.row, unionSeverity))
		if rank != 0 {
			return rank
		}
		return strings.Compare(a.signature, b.signature)
	})
	findings, checks, sources := unionRows(ordered)
	review := jq.NewObj("schema_version", "2", "execution", jq.NewObj("id", execution, "finders", 2),
		"findings", findings, "receipts", receipts, "spec_checks", specs, "summary",
		fmt.Sprintf("Two-finder union: %d findings.", len(findings)))
	return review, jq.NewObj("checks", checks, "sources", sources), nil
}

func cloneUnionRow(row *jq.Obj) *jq.Obj {
	copyRow := jq.NewObj()
	for _, key := range row.Keys {
		copyRow.Set(key, row.Fields[key])
	}
	return copyRow
}

func quoteSide(row *jq.Obj) string {
	side := jq.StrOf(readcommon.FieldOf(row, "side"))
	if side == "" {
		return "RIGHT"
	}
	return side
}

func appendUnionRows(out []any, seen map[string]bool, review *jq.Obj, key string) []any {
	rows, _ := review.Get(key)
	for _, row := range jq.Elements(rows) {
		encoded := jq.Compact(row)
		if !seen[encoded] {
			seen[encoded] = true
			out = append(out, row)
		}
	}
	return out
}

func mergeDuplicate(prior *unionCandidate, row, origin *jq.Obj, verified bool) {
	prior.origins = append(prior.origins, origin)
	severity := readcommon.FieldOf(prior.row, unionSeverity)
	if core.TierRank(readcommon.FieldOf(row, unionSeverity)) < core.TierRank(severity) {
		severity = readcommon.FieldOf(row, unionSeverity)
	}
	if verified && !prior.verified {
		prior.row, prior.verified = row, true
	}
	copyRow := cloneUnionRow(prior.row)
	copyRow.Set(unionSeverity, severity)
	prior.row = copyRow
}

func unionRows(ordered []*unionCandidate) ([]any, []any, []any) {
	findings, checks, sources := []any{}, []any{}, []any{}
	for _, candidate := range ordered {
		findings = append(findings, cloneUnionRow(candidate.row))
		sources = append(sources, jq.NewObj("sig", candidate.signature, "origins", candidate.origins))
		for _, origin := range candidate.origins {
			finder := jq.StrOf(readcommon.FieldOf(origin, "finder"))
			checker := "finder-1"
			if finder == checker {
				checker = "finder-2"
			}
			checks = append(checks, jq.NewObj("sig", candidate.signature, "origin", finder, "checker", checker))
		}
	}
	return findings, checks, sources
}
