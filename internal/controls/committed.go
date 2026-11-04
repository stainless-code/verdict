package controls

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/statedir"
)

// Committed controls layout (prototype): verdict.toml carries the scalars,
// verdict/ carries the file corpus. Base reads go through `git show`.
const (
	committedTOML = "verdict.toml"
	committedDir  = "verdict"
)

// committedControls is one committed/base control set plus the drift
// warnings the worktree copy earns.
type committedControls struct {
	contents map[string][]byte
	warnings []string
}

// readCommittedBase reads the committed controls at base. A missing path is
// an absent control; an unreadable base is controls-unavailable.
func readCommittedBase(ctx context.Context, cmd core.Commander, base, root string) (*committedControls, *core.RefusalError) {
	if _, err := runGit(ctx, cmd, 0, "cat-file", "-e", base+"^{commit}"); err != nil {
		return nil, refuse(CodeUnavailable, fmt.Sprintf("PR base %s is not readable here: %v", shortSHA(base), err))
	}
	c := &committedControls{contents: map[string][]byte{}}
	baseBytes := map[string][]byte{}
	total := 0
	if raw, ok := show(ctx, cmd, base, committedTOML); ok {
		total += len(raw)
		baseBytes[committedTOML] = raw
		for key, inventory := range scalarKeys {
			if v, found := statedir.ParseTOML(raw)[key]; found {
				c.contents[inventory] = []byte(v + "\n")
			}
		}
	}
	for _, rel := range committedFiles(ctx, cmd, base) {
		raw, ok := show(ctx, cmd, base, rel)
		if !ok {
			continue
		}
		if total += len(raw); total > MaxTotalBytes {
			return nil, refuse(CodeTooLarge, fmt.Sprintf("controls exceed the %d-byte total cap — trim them at base", MaxTotalBytes))
		}
		baseBytes[rel] = raw
		c.contents[committedKey(rel)] = raw
	}
	c.warnings = driftWarnings(root, baseBytes)
	return c, nil
}

// scalarKeys maps verdict.toml keys onto the inventory names consumers read.
var scalarKeys = map[string]string{
	"review.panel":      ".review/panel",
	"review.budget_usd": ".review/budget",
	"review.max_bytes":  ".review/max_bytes",
	"review.chunk":      ".review/chunk",
	"review.kb_policy":  ".review/kb-policy",
	"kb.policy":         ".review/kb-policy",
}

// committedFiles lists the committed control corpus under verdict/.
func committedFiles(ctx context.Context, cmd core.Commander, base string) []string {
	out, err := runGit(ctx, cmd, 0, "ls-tree", "-r", "--name-only", base, "--", committedDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || committedKey(line) == "" {
			continue
		}
		files = append(files, line)
	}
	sort.Strings(files)
	return files
}

// committedKey maps one committed path onto its inventory key ("" = not a
// control).
func committedKey(rel string) string {
	switch rel {
	case committedDir + "/kb.jsonl":
		return ".review/kb.jsonl"
	case committedDir + "/learnings.md":
		return ".review/learnings.md"
	}
	if name, ok := strings.CutPrefix(rel, committedDir+"/overrides/"); ok && name != "" && !strings.Contains(name, "/") {
		return ".review/overrides/" + name
	}
	return ""
}

func show(ctx context.Context, cmd core.Commander, base, rel string) ([]byte, bool) {
	out, err := runGit(ctx, cmd, MaxFileBytes+1, "show", base+":"+rel)
	if err != nil || len(out) > MaxFileBytes {
		return nil, false
	}
	return []byte(out), true
}

// driftWarnings compares the worktree copies of the committed controls with
// the base bytes. A difference is a warning, never a silent switch: the run
// uses the base. Extra worktree controls are ignored for the same reason.
func driftWarnings(root string, base map[string][]byte) []string {
	if root == "" {
		root = "."
	}
	var differ, added []string
	for rel, raw := range base {
		local, err := os.ReadFile(filepath.Join(root, rel))
		if err == nil && string(local) != string(raw) {
			differ = append(differ, rel)
		}
	}
	worktree := []string{committedTOML}
	if matches, _ := filepath.Glob(filepath.Join(root, committedDir, "**", "*")); len(matches) > 0 {
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				continue
			}
			if fi, serr := os.Stat(m); serr == nil && !fi.IsDir() {
				worktree = append(worktree, filepath.ToSlash(rel))
			}
		}
	}
	for _, rel := range worktree {
		if _, ok := base[rel]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			added = append(added, rel)
		}
	}
	sort.Strings(differ)
	sort.Strings(added)
	var out []string
	if len(differ) > 0 {
		out = append(out, fmt.Sprintf("controls differ from base: %s (the review uses the base copy)", strings.Join(differ, ", ")))
	}
	if len(added) > 0 {
		out = append(out, fmt.Sprintf("controls not at base: %s (ignored: only base controls judge)", strings.Join(added, ", ")))
	}
	return out
}

// committedHead reads the worktree committed controls (the explicit
// --allow-head-controls opt-out in committed mode).
func committedHead(root string) map[string][]byte {
	if root == "" {
		root = "."
	}
	contents := map[string][]byte{}
	if raw, err := os.ReadFile(filepath.Join(root, committedTOML)); err == nil {
		for key, inventory := range scalarKeys {
			if v, found := statedir.ParseTOML(raw)[key]; found {
				contents[inventory] = []byte(v + "\n")
			}
		}
	}
	for _, rel := range []string{committedDir + "/kb.jsonl", committedDir + "/learnings.md"} {
		if raw, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			contents[committedKey(rel)] = raw
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(root, committedDir, "overrides", "*.md")); len(matches) > 0 {
		for _, m := range matches {
			if raw, err := os.ReadFile(m); err == nil {
				contents[".review/overrides/"+filepath.Base(m)] = raw
			}
		}
	}
	return contents
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
