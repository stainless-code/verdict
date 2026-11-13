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
	if receipt := ArtifactTruncation(filepath.Join(dir, "truncation.json")); receipt != nil {
		return receipt
	}
	return SourceTruncation(dir)
}

// SourceTruncation reads diff/file-list receipts without a prior fetch summary.
func SourceTruncation(dir string) *core.Truncation {
	return ArtifactTruncation(filepath.Join(dir, "filesource.json"))
}

// ArtifactTruncation reads one artifact's truncation receipt.
func ArtifactTruncation(path string) *core.Truncation {
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
