package readcommon

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
)

const minSigPrefix = 8

const (
	keySig       = "sig"
	keyVerifyCmd = "verifyCmd"
	patchPath    = "/patch.json"
	verifyPath   = "/verification.json"
)

// VerifyHint reads the rerun command the bundle recorded for one finding: a
// verification.json claim first (the fresher proof), then a patch.json whose
// sig or findingSig names it. Both compare by sig prefix, and both may be
// absent, in which case the hint is nil. show and handoff share this read so a
// handoff carries the same command a fix pass prints.
func VerifyHint(dir, full string) *core.VerifyHint {
	if hint := verificationHint(dir, full); hint != nil {
		return hint
	}
	return patchHint(dir, full)
}

func verificationHint(dir, full string) *core.VerifyHint {
	for _, claim := range verificationClaims(dir + verifyPath) {
		if !sigMatches(stringField(claim, keySig), full) {
			continue
		}
		if hint := hintFrom(claim); hint != nil {
			return hint
		}
	}
	return nil
}

func verificationClaims(path string) []*jq.Obj {
	raw, ok := readOrderedStore(path)
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var claims []*jq.Obj
	for _, item := range items {
		if claim, ok := item.(*jq.Obj); ok {
			claims = append(claims, claim)
		}
	}
	return claims
}

func patchHint(dir, full string) *core.VerifyHint {
	raw, ok := readOrderedStore(dir + patchPath)
	if !ok {
		return nil
	}
	patch, ok := raw.(*jq.Obj)
	if !ok || !patchMatches(patch, full) {
		return nil
	}
	return hintFrom(patch)
}

func patchMatches(patch *jq.Obj, full string) bool {
	for _, key := range []string{keySig, "findingSig"} {
		named := stringField(patch, key)
		if named != "" && !sigMatches(named, full) {
			return false
		}
	}
	return true
}

func hintFrom(m *jq.Obj) *core.VerifyHint {
	command := stringField(m, keyVerifyCmd)
	if command == "" {
		return nil
	}
	return &core.VerifyHint{VerifyCmd: command}
}

func sigMatches(stored, full string) bool {
	if stored == "" || full == "" {
		return false
	}
	if stored == full {
		return true
	}
	return len(stored) >= minSigPrefix && strings.HasPrefix(full, stored)
}

func stringField(m *jq.Obj, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m.Get(key)
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func readOrderedStore(path string) (any, bool) {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	return doc, true
}
