package panelcore

import (
	"bytes"
	"encoding/json"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func sameReview(a, b *jq.Obj) bool {
	left := executionComparable(a)
	right := executionComparable(b)
	return left != nil && right != nil && bytes.Equal(left, right)
}

func executionComparable(doc *jq.Obj) []byte {
	value, err := jq.DecodeOrdered([]byte(doc.MarshalCompact()))
	if err != nil {
		return nil
	}
	clone, ok := value.(*jq.Obj)
	if !ok {
		return nil
	}
	for _, item := range jq.Elements(readcommon.FieldOf(clone, "findings")) {
		row, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		removeDerivedAlias(row, "body", "claim")
		removeDerivedAlias(row, "finder_confidence", "confidence")
	}
	plain, err := jq.Decode([]byte(clone.MarshalCompact()))
	if err != nil {
		return nil
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		return nil
	}
	return raw
}

func removeDerivedAlias(row *jq.Obj, alias, source string) {
	actual, has := row.Get(alias)
	original, present := row.Get(source)
	actualText, actualOK := actual.(string)
	originalText, originalOK := original.(string)
	if has && present && actualOK && originalOK && actualText == originalText {
		row.Delete(alias)
	}
}
