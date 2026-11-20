package readcommon

import (
	"errors"
	"fmt"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/repro"
)

// ReproductionWarnings checks retained evidence without executing its command.
func ReproductionWarnings(dir string, entries []any) []string {
	var warnings []string
	for _, entry := range entries {
		if FieldOf(entry, "rigor") != "reproduced" {
			continue
		}
		sig := jq.StrOf(FieldOf(entry, "sig"))
		_, err := ReadReproduction(dir, entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("reproduced finding %s has no runnable artifact: %v", sig, err))
		}
	}
	return warnings
}

// ReadReproduction accepts only a finding's validated bundle-local artifact.
func ReadReproduction(dir string, entry any) ([]byte, error) {
	sig := jq.StrOf(FieldOf(entry, "sig"))
	raw, ok := FieldOf(entry, "reproduction").(*jq.Obj)
	if !ok || raw == nil {
		return nil, jq.Sealed("reproduction", errors.New("reproduction missing"))
	}
	path, _ := raw.Get("path")
	name, ok := path.(string)
	if !ok {
		return nil, jq.Sealed("reproduction", errors.New("invalid reproduction path"))
	}
	content, err := repro.Read(dir, sig, name)
	if err != nil {
		return nil, jq.Sealed("read reproduction", err)
	}
	return content, nil
}
