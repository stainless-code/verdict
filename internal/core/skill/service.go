// Package skill is the transport-neutral `verdict skill` service: serve the
// embedded SKILL.md, list the payload, and install it with a receipt. The
// payload is embedded in the binary itself (package skillfs at the repo root),
// so a served skill can never skew from the binary that served it. This
// package never writes stdout/stderr or exits; cmd/verdict owns argv, streams,
// envelopes, and exit codes.
package skill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Payload, receipt, and install status vocabulary.
const (
	Name          = "verdict"
	ReceiptName   = ".verdict-managed"
	StatusCreate  = "create"
	StatusUpdate  = "update"
	StatusKeep    = "keep"
	StatusBlocked = "blocked"
)

const (
	codeBadDir     = "skill-bad-dir"
	codeNoHome     = "skill-no-home"
	codeBadAgent   = "skill-unknown-agent"
	codeForeign    = "skill-dir-foreign"
	codeTargetFile = "skill-target-file"
	codeConflict   = "skill-foreign-conflict"
	codeInstall    = "skill-install-failed"
	codeRead       = "skill-read-failed"
	codePath       = "skill-path-not-inventory"
	codeNotInstall = "skill-not-installed"
	codeCanceled   = "canceled"
	dirPerm        = 0o755
)

const (
	payloadPromptsDir = "prompts"
	payloadSchemasDir = "schemas"
	payloadScriptsDir = "scripts"
)

const skillsSlot = "skills"

const agentShared = "agents"

const (
	agentOpencode = "opencode"
	agentGoose    = "goose"
	agentWindsurf = "windsurf"
)

// agentNames is the supported `--agent` vocabulary (the installer's nine plus
// Codex, which keeps a project skills dir of its own).
var agentNames = []string{
	agentShared, "claude", "codex", "cursor", agentOpencode,
	"copilot", "gemini", agentGoose, "qwen", agentWindsurf,
}

// Request is one skill invocation. Dir/Agent/Force/DryRun are read only by
// install; Version is stamped into the inventory and the receipt.
type Request struct {
	*core.Runtime
	Version string
	Dir     string
	Agent   string
	Force   bool
	DryRun  bool
}

// Inventory is the served skill metadata plus its embedded file list.
type Inventory struct {
	Name    string
	Version string
	Files   []string
}

// Plan is an install preview or outcome. Agent names the target's agent in a
// --detect run and is empty for a single-target install.
type Plan struct {
	Name           string
	Version        string
	Agent          string
	Target         string
	Status         string
	ReceiptVersion string
	Files          []string
	Changed        []string
	Written        int
	ReceiptUpdated bool
	TargetFile     bool
}

// Show returns the embedded SKILL.md bytes.
func Show(_ context.Context, _ *Request) ([]byte, *core.RefusalError) {
	body, err := skillfs.Read(skillfs.SkillMD)
	if err != nil {
		return nil, refuse(codeRead, err.Error())
	}
	return body, nil
}

// ShowFile returns one embedded payload file by its slash path. The path must
// be in the payload inventory; a traversal, absolute, or unknown path refuses
// skill-path-not-inventory.
func ShowFile(ctx context.Context, req *Request, path string) ([]byte, *core.RefusalError) {
	inv, ref := List(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if !inventoryPath(path) || !slices.Contains(inv.Files, path) {
		return nil, refuse(codePath, "not an embedded skill path: "+path)
	}
	body, err := skillfs.Read(path)
	if err != nil {
		return nil, refuse(codeRead, err.Error())
	}
	return body, nil
}

func inventoryPath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	clean := pathpkg.Clean(path)
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// List returns the embedded payload inventory (paths sorted).
func List(_ context.Context, req *Request) (Inventory, *core.RefusalError) {
	files, err := skillfs.Paths()
	if err != nil {
		return Inventory{}, refuse(codeRead, err.Error())
	}
	return Inventory{Name: Name, Version: req.version(), Files: files}, nil
}

// Install plans one install and, unless DryRun, applies it. A foreign target
// refuses typed unless Force; a target that is not a directory always refuses
// on apply (nothing foreign is ever replaced). Keep refreshes only a skewed
// receipt.
func Install(ctx context.Context, req *Request) (Plan, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return Plan{}, refuse(codeCanceled, "canceled before skill install completed")
	}
	plan, ref := planInstall(ctx, req)
	if ref != nil {
		return Plan{}, ref
	}
	if req.DryRun {
		return plan, nil
	}
	if ref := checkApply(req, &plan); ref != nil {
		return Plan{}, ref
	}
	if ref := apply(req, &plan); ref != nil {
		return Plan{}, ref
	}
	return plan, nil
}

func checkApply(req *Request, plan *Plan) *core.RefusalError {
	if plan.TargetFile {
		return refuse(codeTargetFile, plan.Target+" exists and is not a directory; refusing to replace it")
	}
	if plan.Status == StatusBlocked && !req.Force {
		return refuse(codeForeign, plan.Target+" is a non-verdict skill dir (no "+ReceiptName+
			" receipt); re-run with --force to write only verdict's payload files")
	}
	return nil
}

func planInstall(ctx context.Context, req *Request) (Plan, *core.RefusalError) {
	files, ref := List(ctx, req)
	if ref != nil {
		return Plan{}, ref
	}
	target, ref := req.target()
	if ref != nil {
		return Plan{}, ref
	}
	return planAt(files, target)
}

func planAt(files Inventory, target string) (Plan, *core.RefusalError) {
	plan := Plan{
		Name: files.Name, Version: files.Version, Target: target, Files: files.Files,
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		plan.Status = StatusCreate
		plan.Changed = append([]string(nil), files.Files...)
		return plan, nil
	}
	if err != nil {
		return Plan{}, refuse(codeInstall, fmt.Sprintf("cannot inspect %s: %s", target, err))
	}
	if !info.IsDir() {
		plan.Status = StatusBlocked
		plan.TargetFile = true
		return plan, nil
	}
	version, owned := readReceipt(filepath.Join(target, ReceiptName))
	if !owned {
		plan.Status = StatusBlocked
		return plan, nil
	}
	plan.ReceiptVersion = version
	plan.Changed = changedFiles(target, files.Files)
	if len(plan.Changed) == 0 {
		plan.Status = StatusKeep
	} else {
		plan.Status = StatusUpdate
	}
	return plan, nil
}

func (req *Request) target() (string, *core.RefusalError) {
	if req.Dir != "" {
		if !filepath.IsAbs(req.Dir) {
			return "", refuse(codeBadDir, "--dir must be an absolute skills dir: "+req.Dir)
		}
		return filepath.Join(req.Dir, Name), nil
	}
	agent := req.Agent
	if agent == "" {
		agent = agentShared
	}
	dir, known := req.agentDir(agent)
	if !known {
		return "", refuse(codeBadAgent,
			fmt.Sprintf("unknown agent %q (want one of %s)", agent, strings.Join(agentNames, ", ")))
	}
	if dir == "" {
		return "", refuse(codeNoHome,
			"--agent needs HOME (or CLAUDE_CONFIG_DIR/CODEX_HOME) to resolve the skills dir; pass --dir instead")
	}
	return filepath.Join(dir, Name), nil
}

type agentLayout struct {
	base  string
	parts []string
}

func (req *Request) agentDir(agent string) (string, bool) {
	home := req.env("HOME")
	config := req.env("XDG_CONFIG_HOME")
	if config == "" && home != "" {
		config = filepath.Join(home, ".config")
	}
	layouts := map[string]agentLayout{
		agentShared:   {home, []string{".agents", skillsSlot}},
		"claude":      {claudeBase(req, home), []string{skillsSlot}},
		"codex":       {codexBase(req, home), []string{skillsSlot}},
		"cursor":      {home, []string{".cursor", skillsSlot}},
		agentOpencode: {config, []string{agentOpencode, skillsSlot}},
		"copilot":     {home, []string{".copilot", skillsSlot}},
		"gemini":      {home, []string{".gemini", skillsSlot}},
		agentGoose:    {config, []string{agentGoose, skillsSlot}},
		"qwen":        {home, []string{".qwen", skillsSlot}},
		"windsurf":    {home, []string{".codeium", "windsurf", skillsSlot}},
	}
	layout, ok := layouts[agent]
	if !ok {
		return "", false
	}
	return joinBase(layout.base, layout.parts...), true
}

func claudeBase(req *Request, home string) string {
	if dir := req.env("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return joinBase(home, ".claude")
}

func codexBase(req *Request, home string) string {
	if dir := req.env("CODEX_HOME"); dir != "" {
		return dir
	}
	return joinBase(home, ".codex")
}

func joinBase(base string, parts ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

func apply(req *Request, plan *Plan) *core.RefusalError {
	if plan.Status == StatusKeep {
		return refreshReceipt(req, plan)
	}
	if ref := preflight(plan.Target, plan.Files); ref != nil {
		return ref
	}
	if err := os.MkdirAll(plan.Target, dirPerm); err != nil {
		return refuse(codeInstall, fmt.Sprintf("cannot create %s: %s", plan.Target, err))
	}
	for _, rel := range plan.Files {
		if err := writePayload(plan.Target, rel); err != nil {
			return err
		}
	}
	if err := writeReceipt(plan.Target, req.version(), req.TimeUTC()); err != nil {
		return refuse(codeInstall, fmt.Sprintf("cannot write %s/%s: %s", plan.Target, ReceiptName, err))
	}
	plan.Written = len(plan.Files)
	plan.ReceiptVersion = req.version()
	return nil
}

func refreshReceipt(req *Request, plan *Plan) *core.RefusalError {
	if plan.ReceiptVersion == req.version() {
		return nil
	}
	if err := writeReceipt(plan.Target, req.version(), req.TimeUTC()); err != nil {
		return refuse(codeInstall, fmt.Sprintf("cannot refresh %s/%s: %s", plan.Target, ReceiptName, err))
	}
	plan.ReceiptVersion = req.version()
	plan.ReceiptUpdated = true
	return nil
}

func writePayload(target, rel string) *core.RefusalError {
	body, err := skillfs.Read(rel)
	if err != nil {
		return refuse(codeRead, err.Error())
	}
	dst := filepath.Join(target, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return refuse(codeInstall, fmt.Sprintf("cannot create %s: %s", filepath.Dir(dst), err))
	}
	if err := atomic.WriteFileMode(dst, body, skillfs.Mode(rel)); err != nil {
		return refuse(codeInstall, fmt.Sprintf("cannot write %s: %s", dst, err))
	}
	return nil
}

func preflight(target string, files []string) *core.RefusalError {
	if ref := preflightDirs(target); ref != nil {
		return ref
	}
	return preflightFiles(target, files)
}

func preflightDirs(target string) *core.RefusalError {
	for _, dir := range []string{payloadPromptsDir, payloadSchemasDir, payloadScriptsDir} {
		path := filepath.Join(target, dir)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return refuse(codeConflict, path+" exists and is not a directory; refusing to replace it")
		}
	}
	return nil
}

func preflightFiles(target string, files []string) *core.RefusalError {
	for _, rel := range files {
		path := filepath.Join(target, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return refuse(codeConflict, path+" is a directory; refusing to replace it")
		}
	}
	return nil
}

func changedFiles(target string, files []string) []string {
	var changed []string
	for _, rel := range files {
		want, err := skillfs.Read(rel)
		if err != nil {
			changed = append(changed, rel)
			continue
		}
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, want) {
			changed = append(changed, rel)
		}
	}
	return changed
}

func readReceipt(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	doc, err := jq.DecodeObject(raw)
	if err != nil || doc["tool"] != Name {
		return "", false
	}
	return jq.StrOf(doc["version"]), true
}

func writeReceipt(target, version, now string) error {
	doc := jq.NewObj("tool", Name, "version", version, "installed_at", now)
	return atomic.WriteFile(filepath.Join(target, ReceiptName), []byte(doc.MarshalPretty()+"\n"))
}

func (req *Request) version() string {
	if req.Version == "" {
		return core.VersionUnknown
	}
	return req.Version
}

func (req *Request) env(key string) string {
	if req.Runtime == nil {
		return ""
	}
	return req.Env[key]
}

func refuse(code, message string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: message}
}
