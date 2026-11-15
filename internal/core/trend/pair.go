package trendcore

import (
	"os"
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
)

const unknownPair = "unknown"

func bundlePair(bundle string) (string, []string) {
	raw, err := os.ReadFile(bundle + "record.json")
	if err != nil {
		return unknownPair, nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return unknownPair, nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return unknownPair, nil
	}
	return pairOf(m)
}

func pairOf(m map[string]any) (string, []string) {
	author := unknownPair
	if a, ok := jq.AsMap(m["author"]); ok {
		if agent := jq.StrOf(a["agent"]); agent != "" {
			author = agent
		}
	}
	var reviewers []string
	for _, v := range jq.Elements(m["review_models"]) {
		if name := jq.StrOf(v); name != "" {
			reviewers = append(reviewers, name)
		}
	}
	slices.Sort(reviewers)
	return author, slices.Compact(reviewers)
}

func pairKeys(r *row) []string {
	if len(r.reviewers) == 0 {
		return []string{r.author + "/" + unknownPair}
	}
	keys := make([]string, 0, len(r.reviewers))
	for _, model := range r.reviewers {
		keys = append(keys, r.author+"/"+model)
	}
	return keys
}
