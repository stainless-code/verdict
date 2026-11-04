// Package release embeds the frozen release artifact contract so the
// verifier and the GoReleaser generators share one source of artifact
// names, checksum files, and cosign identity.
package release

import _ "embed"

// contractJSON is release/contract.json, embedded at build time.
//
//go:embed contract.json
var contractJSON []byte

// ContractJSON returns the release contract bytes exactly as shipped.
func ContractJSON() []byte { return contractJSON }
