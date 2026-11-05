package recordcore

import (
	"bytes"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func (st *state) validateInputs() error {
	if err := checkVerificationShape(st.dir); err != nil {
		return err
	}
	return checkPriorShape(st.dir)
}

func checkVerificationShape(dir string) error {
	raw, err := readAgentFile(dir + "/verification.json")
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return ref
		}
		raw, _ = ReadOptional(dir + "/verification.json")
	}
	return checkVerificationClaims(raw)
}

func checkVerificationClaims(raw []byte) error {
	if raw == nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		//nolint:nilerr // undecodable evidence reads as "no claims", never a refusal
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedVerifyClaims}
	}
	return checkClaimItems(items)
}

func checkClaimItems(items []any) error {
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectVerifyClaim}
		}
	}
	return nil
}

func checkPriorShape(dir string) error {
	path := dir + "/findings.json"
	raw, ok := ReadOptional(path)
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	if doc == nil || doc == false {
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	return checkPriorElements(path, items)
}

func checkPriorElements(path string, items []any) error {
	for _, item := range items {
		if !priorElementOK(item) {
			return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
		}
	}
	return nil
}

func priorElementOK(item any) bool {
	m, ok := item.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	return priorScalarsOK(m) && priorMapsOK(m)
}

func priorScalarsOK(m *jq.Obj) bool {
	for _, key := range []string{fieldSig, fieldBody, fieldHeading, "kind", fieldSuggestion} {
		if v, has := m.Get(key); has && v != nil && v != false {
			if _, ok := v.(string); !ok {
				return false
			}
		}
	}
	return true
}

func priorMapsOK(m *jq.Obj) bool {
	for _, key := range []string{fieldSeverity, fieldRigor} {
		if v, has := m.Get(key); has && isContainer(v) {
			return false
		}
	}
	return true
}

func isContainer(v any) bool {
	switch v.(type) {
	case *jq.Obj, map[string]any, []any:
		return true
	default:
		return false
	}
}
