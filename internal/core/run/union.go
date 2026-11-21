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
	unionFinder      = "finder"
	unionReceipts    = "receipts"
	unionSpecChecks  = "spec_checks"
)

func discoveryUnion(execution string, diff []byte, reviews []*jq.Obj) (*jq.Obj, *jq.Obj, error) {
	candidates := make(map[string]*unionCandidate)
	idx := evidence.IndexDiff(diff)
	attestations := &unionAttestations{rows: make(map[string][]any), seen: make(map[string]bool)}
	for i, review := range reviews {
		if review == nil {
			continue
		}
		if err := mergeReview(candidates, idx, attestations, i, review); err != nil {
			return nil, nil, err
		}
	}
	if len(candidates) > maxUnionFindings {
		return nil, nil, jq.NewErrf("union exceeds 100 findings")
	}
	return renderUnion(execution, candidates, attestations)
}

func mergeReview(candidates map[string]*unionCandidate, idx *evidence.DiffIndex,
	attestations *unionAttestations, index int, review *jq.Obj,
) error {
	finder := fmt.Sprintf("finder-%d", index+1)
	rows, _ := review.Get("findings")
	if err := mergeDiscovery(candidates, idx, finder, jq.Elements(rows)); err != nil {
		return err
	}
	for _, key := range attestationKeys {
		attestations.merge(review, key)
	}
	return nil
}

// unionAttestations collects what each finder attests it read: the receipts and
// spec checks the union has always carried, plus the coverage ranges and chunk
// names the aggregate review owes on its own. record --require-coverage and the
// chunked gate read the aggregate, so a claim kept only in a finder artifact is
// a claim the aggregate never made.
type unionAttestations struct {
	rows map[string][]any
	seen map[string]bool
}

// attestationKeys are the review fields a finder attests and the aggregate
// carries. receipts and spec_checks are the ones it has always carried;
// coverage and chunks are written only when a finder supplied them.
var attestationKeys = []string{unionReceipts, unionSpecChecks, "coverage", "chunks"}

// merge folds one finder's rows for key into the aggregate, dropping tuples it
// already carries: two finders attesting one hunk, one claim, or one chunk is
// one attestation.
func (a *unionAttestations) merge(review *jq.Obj, key string) {
	rows, _ := review.Get(key)
	for _, row := range jq.Elements(rows) {
		encoded := key + "\x00" + unionRowKey(row, key)
		if a.seen[encoded] {
			continue
		}
		a.seen[encoded] = true
		a.rows[key] = append(a.rows[key], row)
	}
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
		origin := jq.NewObj(unionFinder, finder, "index", n, "finding", row)
		prior := candidates[signature]
		if prior == nil {
			candidates[signature] = &unionCandidate{row: row, origins: []any{origin}, signature: signature, verified: verified}
			continue
		}
		mergeDuplicate(prior, row, origin, verified)
	}
	return nil
}

func renderUnion(execution string, candidates map[string]*unionCandidate, attestations *unionAttestations) (*jq.Obj, *jq.Obj, error) {
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
		"findings", findings, unionReceipts, attestations.rows[unionReceipts], unionSpecChecks,
		attestations.rows[unionSpecChecks], "summary",
		fmt.Sprintf("Two-finder union: %d findings.", len(findings)))
	attestations.attach(review)
	return review, jq.NewObj("checks", checks, "sources", sources), nil
}

// attach writes the read attestations some finder supplied. A bundle no finder
// attested keeps the fields absent, which is how an unattested aggregate
// reaches the coverage gate.
func (a *unionAttestations) attach(review *jq.Obj) {
	for _, key := range []string{"coverage", "chunks"} {
		if rows := a.rows[key]; len(rows) > 0 {
			review.Set(key, rows)
		}
	}
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

func mergeDuplicate(prior *unionCandidate, row, origin *jq.Obj, verified bool) {
	// One finder contributes one origin per signature, so a report of the same
	// path and claim at two anchors stays one finding: the plan emits one
	// directed check per origin, and a repeated origin would repeat the check.
	if !hasOrigin(prior.origins, jq.StrOf(readcommon.FieldOf(origin, unionFinder))) {
		prior.origins = append(prior.origins, origin)
	}
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

func hasOrigin(origins []any, finder string) bool {
	for _, origin := range origins {
		if readcommon.FieldOf(origin, unionFinder) == finder {
			return true
		}
	}
	return false
}

func unionRows(ordered []*unionCandidate) ([]any, []any, []any) {
	findings, checks, sources := []any{}, []any{}, []any{}
	for _, candidate := range ordered {
		findings = append(findings, cloneUnionRow(candidate.row))
		sources = append(sources, jq.NewObj("sig", candidate.signature, "origins", candidate.origins))
		checks = append(checks, unionChecks(candidate)...)
	}
	return findings, checks, sources
}

func unionChecks(candidate *unionCandidate) []any {
	checks := make([]any, 0, len(candidate.origins))
	for _, origin := range candidate.origins {
		finder := jq.StrOf(readcommon.FieldOf(origin, unionFinder))
		checker := "finder-1"
		if finder == checker {
			checker = "finder-2"
		}
		checks = append(checks, jq.NewObj("sig", candidate.signature, "origin", finder, "checker", checker))
	}
	return checks
}

func unionRowKey(row any, key string) string {
	if key == unionReceipts {
		return jq.StrOf(readcommon.FieldOf(row, "hunk")) + "\x00" + jq.StrOf(readcommon.FieldOf(row, "quote"))
	}
	return jq.Compact(row)
}
