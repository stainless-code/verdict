package readcommon

import (
	"io"
	"os"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// ReadConfined reads a capped regular file without following path symlinks.
func ReadConfined(name string, capBytes int64) ([]byte, error) {
	root, leaf, err := OpenParentWith(name, os.OpenRoot)
	if err != nil {
		return nil, jq.Sealed("open parent", err)
	}
	defer root.Close()
	f, err := OpenRegularNoSymlinkIn(root, leaf)
	if err != nil {
		return nil, jq.Sealed("open file", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, capBytes+1))
	if err != nil {
		return nil, jq.Sealed("read file", err)
	}
	if int64(len(raw)) > capBytes {
		return nil, jq.NewErr("file exceeds read cap")
	}
	return raw, nil
}

// WithheldSource returns a valid receipt's source; legacy values read empty.
func WithheldSource(value any) string {
	w := FieldOf(value, "withheld")
	source := jq.StrOf(FieldOf(w, "source"))
	if jq.StrOf(FieldOf(w, "reason")) == "" || jq.StrOf(FieldOf(w, "at")) == "" {
		return ""
	}
	switch source {
	case "exclusion", "agent", "below-severity", "validation":
		return source
	}
	return ""
}

// WithheldCounts includes explicit zeros for legacy stores.
func WithheldCounts(entries []any) map[string]int {
	out := map[string]int{"exclusion": 0, "agent": 0, "below-severity": 0, "validation": 0}
	for _, e := range entries {
		if source := WithheldSource(e); source != "" {
			out[source]++
		}
	}
	return out
}

// VisibleFindings drops entries carrying valid policy withholding receipts.
func VisibleFindings(entries []any) []any {
	var out []any
	for _, e := range entries {
		if WithheldSource(e) == "" {
			out = append(out, e)
		}
	}
	return out
}

// UnlabelledUnderFloor reports whether entry is a visible, non-moved finding
// with no severity under an active floor.
func UnlabelledUnderFloor(entry any, floor string) bool {
	return FieldOf(entry, "heading") != core.MovedSectionHeading &&
		WithheldSource(entry) == "" &&
		config.SeverityRank(floor) > 0 &&
		config.SeverityRank(jq.StrOf(FieldOf(entry, "severity"))) == 0
}
