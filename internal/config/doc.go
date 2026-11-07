// Package config owns the typed repository configuration foundation: the
// strict `.verdict/config.json` parser, the personal settings model, the
// portable path-scope matcher, the pure default and requirement resolver
// with clause provenance, the captured evidence shape, the `.verdict/state`
// layout helpers, and the trusted-base source adapter.
//
// The package is pure where it matters: parsing and resolution take
// explicit inputs and return explicit outputs with no filesystem, process,
// or network access. I/O lives in the boundary adapters (source.go reads
// the trusted base through the D1 seam; state.go writes the ignore file),
// so later consumer slices (fetch, record, post, report, MCP, TypeScript,
// router) call Resolve and Evidence without inheriting side effects.
//
// The package imports internal/core for the canonical wire types and the
// typed refusal, and internal/controls for the trusted-base read seam. It
// never imports a transport or verb package, keeping the core import
// boundary intact.
package config
