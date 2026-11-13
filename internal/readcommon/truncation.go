package readcommon

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// BundleTruncation reads the producer's receipt from bounded source artifacts.
// Missing artifacts have no receipt; an oversized artifact is incomplete.
func BundleTruncation(dir string) *core.Truncation {
	if receipt := artifactTruncation(filepath.Join(dir, "truncation.json")); receipt != nil {
		return receipt
	}
	return SourceTruncation(dir)
}

// SourceTruncation reads source receipts without a prior fetch summary.
func SourceTruncation(dir string) *core.Truncation {
	for _, name := range []string{"filesource.json", "threads.json", "impact.json"} {
		if receipt := artifactTruncation(filepath.Join(dir, name)); receipt != nil {
			return receipt
		}
	}
	return nil
}

func artifactTruncation(path string) *core.Truncation {
	raw, err := jq.CappedFile(path, jq.MaxFindingsBytes)
	if errors.Is(err, jq.ErrTooLarge) {
		return core.ScanCut(true, 0)
	}
	if err != nil {
		return nil
	}
	var receipt core.Truncation
	if json.Unmarshal(raw, &receipt) == nil && receipt.Truncated {
		return &receipt
	}
	return nil
}
