package skill

import (
	"context"
	"errors"
	"fmt"
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
func Uninstall(ctx context.Context, req *Request) (Removal, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return Removal{}, refuse(codeCanceled, "canceled before skill uninstall completed")
	}
	rm, ref := planRemoval(ctx, req)
	if ref != nil {
		return Removal{}, ref
	}
	if req.DryRun {
		return rm, nil
	}
	if ref := applyRemoval(&rm); ref != nil {
		return Removal{}, ref
	}
	return rm, nil
}

// UninstallAgents removes one target per named agent, in argv order and
// deduped, with every target planned before the first file is deleted.
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
	for _, agent := range agents {
		at := *req
		at.Agent = agent
		rm, ref := planRemoval(ctx, &at)
		if ref != nil {
			return RemovalSet{}, ref
		}
		removals = append(removals, rm)
	}
	set := RemovalSet{Name: files.Name, Version: files.Version, Agents: agents, Targets: removals}
	if req.DryRun {
		return set, nil
	}
	for i := range removals {
		if ref := applyRemoval(&removals[i]); ref != nil {
			return RemovalSet{}, ref
		}
	}
	return set, nil
}

func planRemoval(ctx context.Context, req *Request) (Removal, *core.RefusalError) {
	files, ref := List(ctx, req)
	if ref != nil {
		return Removal{}, ref
	}
	target, ref := req.target()
	if ref != nil {
		return Removal{}, ref
	}
	return planRemovalPath(req, files, target)
}

//
//nolint:revive // the receipt/force/status classification
func planRemovalPath(req *Request, files Inventory, target string) (Removal, *core.RefusalError) {
	rm := Removal{Name: files.Name, Version: files.Version, Agent: req.Agent, Target: target, DryRun: req.DryRun}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return Removal{}, refuse(codeNotInstall, "no installed skill at "+target+" (run `verdict skill install` first)")
	}
	if err != nil {
		return Removal{}, refuse(codeInstall, fmt.Sprintf("cannot inspect %s: %s", target, err))
	}
	if !info.IsDir() {
		return Removal{}, refuse(codeTargetFile, target+" exists and is not a directory; refusing to remove it")
	}
	version, owned := readReceipt(filepath.Join(target, ReceiptName))
	rm.ReceiptVersion = version
	if !owned && !req.Force && !req.DryRun {
		return Removal{}, refuse(codeForeign, target+" is a non-verdict skill dir (no "+ReceiptName+
			" receipt); re-run with --force to remove only verdict's payload files")
	}
	present, ref := ownedPayload(target, files.Files)
	if ref != nil {
		return Removal{}, ref
	}
	rm.Would = present
	rm.Kept = foreignPayload(target, files.Files)
	switch {
	case !owned && !req.Force:
		rm.Status = StatusBlocked
	case len(present) == 0:
		rm.Status = StatusKeep
	default:
		rm.Status = StatusRemove
	}
	return rm, nil
}

func ownedPayload(target string, files []string) ([]string, *core.RefusalError) {
	present := make([]string, 0, len(files))
	for _, rel := range files {
		path := filepath.Join(target, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, refuse(codeInstall, fmt.Sprintf("cannot inspect %s: %s", path, err))
		}
		if !info.Mode().IsRegular() {
			return nil, refuse(codeConflict, path+" is not a regular file; refusing to remove it")
		}
		present = append(present, rel)
	}
	return present, nil
}

//
//nolint:revive // one directory walk classifies owned and foreign files
func foreignPayload(target string, files []string) []string {
	owned := make(map[string]bool, len(files)+1)
	for _, rel := range files {
		owned[rel] = true
	}
	owned[ReceiptName] = true
	var kept []string
	err := filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(target, path)
		if relErr != nil {
			return fmt.Errorf("rel %s: %w", path, relErr)
		}
		if rel = filepath.ToSlash(rel); !owned[rel] {
			kept = append(kept, rel)
		}
		return nil
	})
	if err != nil {
		return kept
	}
	slices.Sort(kept)
	return kept
}

func applyRemoval(rm *Removal) *core.RefusalError {
	for _, rel := range rm.Would {
		path := filepath.Join(rm.Target, filepath.FromSlash(rel))
		if err := os.Remove(path); err != nil {
			return refuse(codeInstall, fmt.Sprintf("cannot remove %s: %s", path, err))
		}
	}
	if err := removeReceipt(rm); err != nil {
		return err
	}
	pruneEmptyDirs(rm.Target)
	rm.Removed = rm.Would
	rm.Would = nil
	if len(rm.Removed) > 0 {
		rm.Status = StatusRemoved
	} else {
		rm.Status = StatusKeep
	}
	return nil
}

func removeReceipt(rm *Removal) *core.RefusalError {
	path := filepath.Join(rm.Target, ReceiptName)
	if _, err := os.Lstat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return refuse(codeInstall, fmt.Sprintf("removed the payload but not the receipt: %s", err))
		}
		rm.ReceiptRemoved = true
	}
	return nil
}

func pruneEmptyDirs(target string) {
	for _, dir := range []string{payloadPromptsDir, payloadSchemasDir, payloadScriptsDir} {
		os.Remove(filepath.Join(target, dir)) // non-empty: a foreign file keeps the directory
	}
	os.Remove(target) // non-empty: a foreign file keeps the directory
}
