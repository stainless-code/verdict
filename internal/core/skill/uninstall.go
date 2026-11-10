package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	// StatusRemove is a dry-run uninstall plan with payload files to remove.
	StatusRemove = "remove"
	// StatusRemoved is a real uninstall that removed payload files.
	StatusRemoved = "removed"
)

// Removal is one target's uninstall report.
type Removal struct {
	Name           string
	Version        string
	Agent          string
	Target         string
	Status         string
	ReceiptVersion string
	Removed        []string
	Would          []string
	Kept           []string
	DryRun         bool
	ReceiptRemoved bool
}

// RemovalSet is an uninstall over several targets.
type RemovalSet struct {
	Name    string
	Version string
	Agents  []string
	Targets []Removal
}

// Uninstall removes the payload files the receipt owns, then the receipt.
// Foreign files survive, and a target without a receipt refuses unless Force.
// Every path is resolved inside the opened target root, so a symlink cannot
// point a stat or a removal outside it; a target that is itself a symlink
// refuses before anything is touched.
func Uninstall(ctx context.Context, req *Request) (Removal, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return Removal{}, refuse(codeCanceled, "canceled before skill uninstall completed")
	}
	rm, root, ref := planRemoval(ctx, req)
	if ref != nil {
		return Removal{}, ref
	}
	defer func() { _ = root.Close() }()
	if req.DryRun {
		return rm, nil
	}
	if ref := applyRemoval(root, &rm); ref != nil {
		return Removal{}, ref
	}
	return rm, nil
}

// UninstallAgents removes one target per named agent, in argv order and
// deduped by resolved target, with every target planned before the first file
// is deleted.
//
//nolint:revive // the multi-target plan-then-apply pass
func UninstallAgents(ctx context.Context, req *Request, agents []string) (RemovalSet, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return RemovalSet{}, refuse(codeCanceled, "canceled before skill uninstall completed")
	}
	files, ref := List(ctx, req)
	if ref != nil {
		return RemovalSet{}, ref
	}
	agents = dedupeAgents(agents)
	removals := make([]Removal, 0, len(agents))
	kept := make([]string, 0, len(agents))
	roots := make([]*os.Root, 0, len(agents))
	seen := make(map[string]bool, len(agents))
	closeRoots := func() {
		for _, root := range roots {
			_ = root.Close()
		}
	}
	for _, agent := range agents {
		target, ref := req.resolvedTarget(agent)
		if ref != nil {
			closeRoots()
			return RemovalSet{}, ref
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		at := *req
		at.Agent = agent
		rm, root, ref := planRemovalPath(&at, files, target)
		if ref != nil {
			closeRoots()
			return RemovalSet{}, ref
		}
		removals = append(removals, rm)
		kept = append(kept, agent)
		roots = append(roots, root)
	}
	set := RemovalSet{Name: files.Name, Version: files.Version, Agents: kept, Targets: removals}
	if req.DryRun {
		closeRoots()
		return set, nil
	}
	defer closeRoots()
	for i := range removals {
		if ref := applyRemoval(roots[i], &removals[i]); ref != nil {
			return RemovalSet{}, ref
		}
	}
	return set, nil
}

func planRemoval(ctx context.Context, req *Request) (Removal, *os.Root, *core.RefusalError) {
	files, ref := List(ctx, req)
	if ref != nil {
		return Removal{}, nil, ref
	}
	target, ref := req.target()
	if ref != nil {
		return Removal{}, nil, ref
	}
	return planRemovalPath(req, files, target)
}

func planRemovalPath(req *Request, files Inventory, target string) (Removal, *os.Root, *core.RefusalError) {
	rm := Removal{Name: files.Name, Version: files.Version, Agent: req.Agent, Target: target, DryRun: req.DryRun}
	root, ref := openTargetRoot(target)
	if ref != nil {
		return Removal{}, nil, ref
	}
	version, owned := readReceiptRoot(root)
	rm.ReceiptVersion = version
	if !owned && !req.Force && !req.DryRun {
		_ = root.Close()
		return Removal{}, nil, refuse(codeForeign, target+" is a non-verdict skill dir (no "+ReceiptName+
			" receipt); re-run with --force to remove only verdict's payload files")
	}
	present, ref := ownedPayload(root, target, files.Files)
	if ref != nil {
		_ = root.Close()
		return Removal{}, nil, ref
	}
	rm.Would = present
	rm.Kept = foreignPayload(root, files.Files)
	switch {
	case !owned && !req.Force:
		rm.Status = StatusBlocked
	case len(present) == 0:
		rm.Status = StatusKeep
	default:
		rm.Status = StatusRemove
	}
	return rm, root, nil
}

func openTargetRoot(target string) (*os.Root, *core.RefusalError) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refuse(codeNotInstall, "no installed skill at "+target+" (run `verdict skill install` first)")
	}
	if err != nil {
		return nil, refuse(codeInstall, fmt.Sprintf("cannot inspect %s: %s", target, err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, refuse(codeTargetFile, target+" is a symlink; refusing to remove through it")
	}
	if !info.IsDir() {
		return nil, refuse(codeTargetFile, target+" exists and is not a directory; refusing to remove it")
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return nil, refuse(codeInstall, fmt.Sprintf("cannot inspect %s: %s", target, err))
	}
	return root, nil
}

func ownedPayload(root *os.Root, target string, files []string) ([]string, *core.RefusalError) {
	present := make([]string, 0, len(files))
	for _, rel := range files {
		path := filepath.Join(target, filepath.FromSlash(rel))
		info, err := root.Lstat(filepath.FromSlash(rel))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, refuse(codeConflict, path+" is not a removable file inside the skill dir; refusing to remove it ("+err.Error()+")")
		}
		if !info.Mode().IsRegular() {
			return nil, refuse(codeConflict, path+" is not a regular file; refusing to remove it")
		}
		present = append(present, rel)
	}
	return present, nil
}

//nolint:revive // one directory walk classifies owned and foreign files
func foreignPayload(root *os.Root, files []string) []string {
	owned := make(map[string]bool, len(files)+1)
	for _, rel := range files {
		owned[rel] = true
	}
	owned[ReceiptName] = true
	var kept []string
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == "." {
			return nil
		}
		if !owned[path] {
			kept = append(kept, path)
		}
		return nil
	})
	if err != nil {
		return kept
	}
	slices.Sort(kept)
	return kept
}

func applyRemoval(root *os.Root, rm *Removal) *core.RefusalError {
	for _, rel := range rm.Would {
		path := filepath.Join(rm.Target, filepath.FromSlash(rel))
		if err := root.Remove(filepath.FromSlash(rel)); err != nil {
			return refuse(codeInstall, fmt.Sprintf("cannot remove %s: %s", path, err))
		}
	}
	if err := removeReceipt(root, rm); err != nil {
		return err
	}
	pruneEmptyDirs(root)
	_ = root.Close()
	removeTargetDir(rm.Target)
	rm.Removed = rm.Would
	rm.Would = nil
	if len(rm.Removed) > 0 {
		rm.Status = StatusRemoved
	} else {
		rm.Status = StatusKeep
	}
	return nil
}

func removeReceipt(root *os.Root, rm *Removal) *core.RefusalError {
	if _, err := root.Lstat(ReceiptName); err == nil {
		if err := root.Remove(ReceiptName); err != nil {
			return refuse(codeInstall, fmt.Sprintf("removed the payload but not the receipt: %s", err))
		}
		rm.ReceiptRemoved = true
	}
	return nil
}

func pruneEmptyDirs(root *os.Root) {
	for _, dir := range []string{payloadPromptsDir, payloadSchemasDir, payloadScriptsDir} {
		root.Remove(dir) //nolint:errcheck // best-effort prune; a foreign file keeps the directory
	}
}

func removeTargetDir(target string) {
	parent, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		return
	}
	defer func() { _ = parent.Close() }()
	parent.Remove(filepath.Base(target)) //nolint:errcheck // best-effort prune; a foreign file keeps the directory
}
