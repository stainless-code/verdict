package release

import (
	"encoding/json"
	"fmt"
)

// Platform is one os/arch pair the release ships an archive for.
type Platform struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

// Platforms returns the archive rows, deduplicated and in contract order. An
// empty result means the contract names no archives.
func Platforms() ([]Platform, error) {
	var doc struct {
		Archives []Platform `json:"archives"`
	}
	if err := json.Unmarshal(contractJSON, &doc); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary; the embedded asset cannot be inspected
		return nil, fmt.Errorf("decode the release contract archives: %v", err)
	}
	seen := make(map[Platform]bool, len(doc.Archives))
	platforms := make([]Platform, 0, len(doc.Archives))
	for _, platform := range doc.Archives {
		if seen[platform] {
			continue
		}
		seen[platform] = true
		platforms = append(platforms, platform)
	}
	return platforms, nil
}
