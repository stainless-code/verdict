package readcommon

import (
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// minSigPrefix is the shortest stored sig prefix VerifyHint matches: sigs are
// 40 hex chars, so 8 is unambiguous within one store.
const minSigPrefix = 8

const (
	keySig        = "sig"
	keyVerifyCmd  = "verifyCmd"
	keyResultHead = "resultHead"
	patchPath     = "/patch.json"
	verifyPath    = "/verification.json"
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

// verificationHint reads the matching claim from the verification log.
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

// verificationClaims reads the log's object entries, skipping anything else.
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

// patchHint reads the patch record's command when it names the finding (or
// names no sig at all, the batch-patch case).
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

// patchMatches holds a patch record to the finding it names.
func patchMatches(patch *jq.Obj, full string) bool {
	for _, key := range []string{keySig, "findingSig"} {
		named := stringField(patch, key)
		if named != "" && !sigMatches(named, full) {
			return false
		}
	}
	return true
}

// hintFrom builds the hint from one record, nil without a command.
func hintFrom(m *jq.Obj) *core.VerifyHint {
	command := stringField(m, keyVerifyCmd)
	if command == "" {
		return nil
	}
	hint := &core.VerifyHint{VerifyCmd: command}
	if head := stringField(m, keyResultHead); head != "" {
		hint.ResultHead = &head
	}
	return hint
}

// sigMatches accepts the full sig or a stored prefix of at least minSigPrefix.
func sigMatches(stored, full string) bool {
	if stored == "" || full == "" {
		return false
	}
	if stored == full {
		return true
	}
	return len(stored) >= minSigPrefix && strings.HasPrefix(full, stored)
}

// stringField reads one string field off a record ("" when absent or not a
// string).
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

// readOrderedStore decodes one store file, keeping key order (false when it is
// absent or unreadable).
func readOrderedStore(path string) (any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	return doc, true
}
