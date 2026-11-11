// Package upgrade is the transport-neutral `verdict upgrade` and `verdict
// uninstall` service. It resolves the running binary through symlinks, maps
// it to one install channel, and acts without prompting: a verified
// self-replace for installer installs, the owning package manager's global
// update for npm/pnpm/bun/yarn/brew, and a typed refusal (with the exact
// remediation command) everywhere else. Every dependency on the outside
// world is injected, so the channel matrix and every refusal are hermetic
// tests; the cmd layer owns argv, streams, envelopes, and exit codes.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Install channels, in the operator's §6 table order.
const (
	ChannelInstaller = "installer-managed"
	ChannelNPM       = "npm"
	ChannelPNPM      = "pnpm"
	ChannelBun       = "bun"
	ChannelYarn      = "yarn"
	ChannelBrew      = "homebrew"
	ChannelProject   = "project-local"
	ChannelSkill     = "skill"
	ChannelUnknown   = "unknown"
)

// Response actions.
const (
	ActionSelfReplace = "self-replace"
	ActionDelegate    = "delegate"
	ActionRefuse      = "refuse"
	ActionUninstall   = "uninstall"
)

const (
	codeProjectLocal   = "upgrade-project-local"
	codeSkillCopy      = "upgrade-skill-copy"
	codeNeedsPrivilege = "upgrade-needs-privilege"
	codeUnidentified   = "upgrade-unidentified"
	codeBadMethod      = "upgrade-bad-method"
	codeBadVersion     = "upgrade-bad-version"
	codeManagerMissing = "manager-missing"
	codeManagerFailed  = "manager-failed"
	codeUpgradeFailed  = "upgrade-failed"
	codeVersionPin     = "upgrade-version-unsupported"
	codeCanceled       = "canceled"
	codeUninstallGate  = "uninstall-foreign"
	codeNoReceipt      = "uninstall-no-receipt"
)

const (
	receiptName      = ".verdict-installer"
	pathMarker       = "# verdict installer"
	npmPackage       = "@stainless-code/verdict"
	brewTarget       = "stainless-code/tap/verdict"
	defaultInstallBy = "install.sh"
	windowsInstallBy = "install.ps1"
	helperCommand    = "__upgrade-helper"
	managerAdd       = "add"
	libDir           = "lib"
	globalDir        = "global"
	nodeModulesDir   = "node_modules"
	nodeModulesSeg   = "/" + nodeModulesDir + "/"
	projectWalkDepth = 8
	commandWaitDelay = 10 * time.Second
	filePermResult   = 0o644
)

var managerPackages = map[string][]string{
	ChannelNPM:  {"npm", "i", "-g"},
	ChannelPNPM: {"pnpm", managerAdd, "-g"},
	ChannelBun:  {"bun", managerAdd, "-g"},
	ChannelYarn: {"yarn", globalDir, managerAdd},
	ChannelBrew: {"brew", "upgrade", brewTarget},
}

// Request is one upgrade or uninstall invocation. The core.Runtime carries
// the injected environment and clock; the remaining fields are adapters
// for tests and the command layer.
type Request struct { //nolint:govet // injected request surface; order documents the knobs
	*core.Runtime
	Client     *http.Client
	RunCosign  func(context.Context, string, []string) ([]byte, error)
	Smoke      func(context.Context, string, string) error
	Exec       func(context.Context, []string, []string, io.Writer, io.Writer) (int, error)
	LookPath   func(string) (string, error)
	EUID       *int
	Stdout     io.Writer
	Stderr     io.Writer
	EnvSlice   []string
	Executable string
	PinVersion string
	GOOS       string
	DryRun     bool
	RemovePath bool
}

// Response is the plan (dry-run) or the completed action.
type Response struct {
	Channel         string
	Action          string
	Reason          string
	Version         string
	Target          string
	SHA256          string
	HelperResult    string
	HelperLog       string
	Removed         []string
	Command         []string
	ExitCode        int
	DryRun          bool
	Handoff         bool
	PathLineRemoved bool
}

type detection struct {
	Channel    string
	InstallDir string
	Receipt    string
	exe        string
	Code       string
	Reason     string
	Command    string
}

// Run upgrades the running verdict binary (or prints the dry-run plan).
func Run(ctx context.Context, req *Request) (Response, *core.RefusalError) {
	det, ref := detect(req)
	if ref != nil {
		return Response{}, ref
	}
	if req.DryRun {
		return plan(req, det), nil
	}
	if det.Code != "" {
		return Response{}, &core.RefusalError{Code: det.Code, Message: det.Reason}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, canceled(err)
	}
	if det.Channel == ChannelInstaller {
		return selfReplace(ctx, req, det)
	}
	return delegate(ctx, req, det)
}

func plan(req *Request, det *detection) Response {
	resp := Response{
		Channel: det.Channel,
		Target:  det.exe,
		DryRun:  true,
		Version: strings.TrimPrefix(req.PinVersion, "v"),
		Reason:  det.Reason,
		Action:  ActionSelfReplace,
	}
	if det.Code != "" {
		resp.Action = ActionRefuse
		if det.Command != "" {
			resp.Command = []string{det.Command}
		}
		return resp
	}
	switch det.Channel {
	case ChannelInstaller:
		if resp.Version == "" {
			resp.Version = "latest"
		}
	case ChannelBrew:
		resp = *planBrew(req, &resp)
	default:
		resp.Action = ActionDelegate
		resp.Command = managerUpdate(det.Channel, req.PinVersion)
	}
	return resp
}

func planBrew(req *Request, resp *Response) *Response {
	if req.PinVersion != "" {
		resp.Action = ActionRefuse
		resp.Reason = "the Homebrew formula owns version selection; run: brew upgrade " + brewTarget
		resp.Command = nil
		return resp
	}
	resp.Action = ActionDelegate
	resp.Command = managerUpdate(ChannelBrew, req.PinVersion)
	return resp
}

func managerUpdate(channel, pin string) []string {
	base, ok := managerPackages[channel]
	if !ok {
		return nil
	}
	argv := append([]string(nil), base...)
	if channel == ChannelBrew {
		return argv
	}
	target := npmPackage + "@latest"
	if pin != "" {
		target = npmPackage + "@" + strings.TrimPrefix(pin, "v")
	}
	return append(argv, target)
}

func detect(req *Request) (*detection, *core.RefusalError) {
	exe, err := resolveExecutable(req)
	if err != nil || exe == "" {
		return nil, &core.RefusalError{
			Code:    codeUnidentified,
			Message: "could not resolve the running verdict binary: " + errText(err) + "; reinstall with: " + installerCommand(),
		}
	}
	det := &detection{Channel: ChannelUnknown, exe: exe, InstallDir: req.installDir()}
	det.Receipt = filepath.Join(det.InstallDir, receiptName)
	adoptInstallerDir(req, det)
	if method := req.env("VERDICT_INSTALL_METHOD"); method != "" {
		return detectOverride(req, det, method)
	}
	det.Channel = classify(req, det)
	return applySafety(req, det)
}

func adoptInstallerDir(req *Request, det *detection) {
	dir := filepath.Dir(det.exe)
	if dir == det.InstallDir {
		return
	}
	if heuristicChannel(req, det.exe) != ChannelUnknown {
		return
	}
	receipt := filepath.Join(dir, receiptName)
	if !receiptNames(receipt, filepath.Base(det.exe)) {
		return
	}
	det.InstallDir = dir
	det.Receipt = receipt
}

func detectOverride(req *Request, det *detection, method string) (*detection, *core.RefusalError) {
	channel, ok := overrideChannel(method)
	if !ok {
		return nil, &core.RefusalError{
			Code: codeBadMethod,
			Message: fmt.Sprintf("VERDICT_INSTALL_METHOD=%s is not one of "+
				"installer-managed|npm|pnpm|bun|yarn|homebrew|project-local|skill|unknown", method),
		}
	}
	det.Channel = channel
	if channel == ChannelInstaller && !validReceipt(det.Receipt) {
		det.Code = codeNoReceipt
		det.Reason = fmt.Sprintf("no installer receipt in %s (not installed by install.sh); "+
			"reinstall with: %s", det.InstallDir, installerCommand())
	}
	return applySafety(req, det)
}

func overrideChannel(method string) (string, bool) {
	switch method {
	case "installer", "installer-managed":
		return ChannelInstaller, true
	case ChannelNPM, ChannelPNPM, ChannelBun, ChannelYarn:
		return method, true
	case "brew", ChannelBrew:
		return ChannelBrew, true
	case ChannelProject:
		return ChannelProject, true
	case ChannelSkill, "skill-copy":
		return ChannelSkill, true
	case "unknown", "unidentified":
		return ChannelUnknown, true
	default:
		return "", false
	}
}

func classify(req *Request, det *detection) string {
	if channel, ok := markerChannel(req); ok {
		return channel
	}
	if within(det.exe, det.InstallDir) && validReceipt(det.Receipt) {
		return ChannelInstaller
	}
	return heuristicChannel(req, det.exe)
}

func markerChannel(req *Request) (string, bool) {
	if req.env("VERDICT_NPM_MANAGED") == "1" {
		return ChannelNPM, true
	}
	if req.env("VERDICT_MANAGED") == "1" {
		return ChannelBrew, true
	}
	return "", false
}

func heuristicChannel(req *Request, exe string) string {
	if channel, ok := managerChannel(req, exe); ok {
		return channel
	}
	switch {
	case isBrewPath(req, exe):
		return ChannelBrew
	case isSkillPath(exe):
		return ChannelSkill
	case strings.Contains(filepath.ToSlash(exe), nodeModulesSeg):
		return ChannelProject
	default:
		return ChannelUnknown
	}
}

func applySafety(req *Request, det *detection) (*detection, *core.RefusalError) {
	if req.euid() == 0 {
		det.Code = codeNeedsPrivilege
		det.Reason = "refusing to self-update as root/admin; run the installer as the owning user: " + installerCommand()
		det.Command = installerCommand()
		return det, nil
	}
	switch det.Channel {
	case ChannelProject:
		root := projectRoot(filepath.Dir(det.exe))
		det.Code = codeProjectLocal
		det.Command = "npm install " + npmPackage + "@latest"
		det.Reason = fmt.Sprintf("project-local install at %s: the upgrade belongs in the project manifest/lockfile; "+
			"run: cd %s && %s", det.exe, shellQuote(root), det.Command)
	case ChannelSkill:
		root := skillRoot(det.exe)
		det.Code = codeSkillCopy
		det.Command = "npx skills update"
		det.Reason = fmt.Sprintf("skill copy at %s: update the skill with `npx skills update`, "+
			"or bootstrap the binary with `./scripts/verdict bootstrap`", det.exe)
		if root != "" {
			det.Reason += " (skill dir: " + root + ")"
		}
	case ChannelInstaller:
		if det.Code == "" && !writableDir(det.InstallDir) {
			det.Code = codeNeedsPrivilege
			det.Command = installerCommand()
			det.Reason = fmt.Sprintf("%s is not writable: reinstall without sudo using: %s", det.InstallDir, det.Command)
		}
	case ChannelNPM, ChannelPNPM, ChannelBun, ChannelYarn, ChannelBrew:
	case ChannelUnknown:
		det.Code = codeUnidentified
		det.Command = installerCommand()
		det.Reason = fmt.Sprintf("could not identify the install channel for %s; reinstall with: %s", det.exe, det.Command)
	}
	return det, nil
}

func managerChannel(req *Request, exe string) (string, bool) {
	for _, candidate := range managerRoots(req.envMap()) {
		if within(exe, candidate.path) {
			return candidate.channel, true
		}
	}
	return "", false
}

type root struct {
	channel string
	path    string
}

func managerRoots(env map[string]string) []root {
	home := homeDir(env)
	roots := []root{}
	add := func(channel, base string, parts ...string) {
		if base == "" {
			return
		}
		roots = append(roots, root{channel, filepath.Join(append([]string{base}, parts...)...)})
	}
	add(ChannelNPM, env["NPM_CONFIG_PREFIX"], libDir, nodeModulesDir)
	add(ChannelNPM, home, ".npm-global", libDir, nodeModulesDir)
	add(ChannelNPM, "/usr/local", libDir, nodeModulesDir)
	add(ChannelNPM, "/usr", libDir, nodeModulesDir)
	add(ChannelNPM, env["HOMEBREW_PREFIX"], libDir, nodeModulesDir)
	add(ChannelNPM, env["APPDATA"], "npm", nodeModulesDir)
	add(ChannelPNPM, env["PNPM_HOME"], globalDir)
	add(ChannelPNPM, home, ".local", "share", "pnpm", globalDir)
	add(ChannelPNPM, home, "Library", "pnpm", globalDir)
	add(ChannelPNPM, env["LOCALAPPDATA"], "pnpm", globalDir)
	add(ChannelBun, env["BUN_INSTALL"], "install", globalDir, nodeModulesDir)
	add(ChannelBun, home, ".bun", "install", globalDir, nodeModulesDir)
	add(ChannelYarn, home, ".config", "yarn", globalDir, nodeModulesDir)
	add(ChannelYarn, home, ".yarn", globalDir, nodeModulesDir)
	add(ChannelYarn, home, "Library", "Application Support", "Yarn", globalDir, nodeModulesDir)
	add(ChannelYarn, env["LOCALAPPDATA"], "Yarn", globalDir, nodeModulesDir)
	return roots
}

func isBrewPath(req *Request, exe string) bool {
	for _, marker := range []string{"/Cellar/", "/Caskroom/"} {
		if strings.Contains(filepath.ToSlash(exe), marker) {
			return true
		}
	}
	prefix := req.env("HOMEBREW_PREFIX")
	return prefix != "" && within(exe, prefix)
}

func isSkillPath(exe string) bool {
	sawSkills := false
	for part := range strings.SplitSeq(filepath.ToSlash(exe), "/") {
		if sawSkills && part == "verdict" {
			return true
		}
		if part == "skills" {
			sawSkills = true
		}
	}
	return false
}

func skillRoot(exe string) string {
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "bin" {
		return filepath.Dir(dir)
	}
	return dir
}

func projectRoot(dir string) string {
	for range projectWalkDepth {
		if hasManifest(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dir
}

func hasManifest(dir string) bool {
	for _, name := range []string{"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func installerCommand() string {
	return "curl -fsSL https://github.com/stainless-code/verdict/releases/latest/download/install.sh | sh"
}

func resolveExecutable(req *Request) (string, error) {
	if req.Executable != "" {
		return filepath.Clean(req.Executable), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", jq.Sealed("os.Executable", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Clean(exe), nil
}

func (req *Request) installDir() string {
	dir := req.env("VERDICT_INSTALL")
	if dir == "" {
		dir = filepath.Join(homeDir(req.envMap()), ".verdict", "bin")
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

func (req *Request) platform() string {
	if req.GOOS != "" {
		return req.GOOS
	}
	return runtime.GOOS
}

func (req *Request) euid() int {
	if req.EUID != nil {
		return *req.EUID
	}
	return os.Geteuid()
}

func (req *Request) env(key string) string {
	return req.envMap()[key]
}

func (req *Request) envMap() map[string]string {
	if req.Runtime == nil {
		return nil
	}
	return req.Env
}

func (req *Request) warn(msg string) {
	if req.Runtime != nil {
		req.Diag("upgrade: " + msg)
	}
}

func (req *Request) human(line string) {
	if req.Runtime != nil {
		req.Human(line)
	}
}

func homeDir(env map[string]string) string {
	if home := env["HOME"]; home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

type receipt struct {
	Binary      string `json:"binary"`
	Version     string `json:"version"`
	InstalledBy string `json:"installed_by"`
}

func validReceipt(path string) bool {
	_, ok := readReceipt(path)
	return ok
}

func receiptNames(path, binary string) bool {
	doc, ok := readReceipt(path)
	return ok && doc.Binary == binary
}

func readReceipt(path string) (receipt, bool) {
	var doc receipt
	raw, err := os.ReadFile(path)
	if err != nil {
		return doc, false
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, false
	}
	if doc.Binary != "verdict" && doc.Binary != "verdict.exe" {
		return doc, false
	}
	return doc, doc.Version != "" && (doc.InstalledBy == defaultInstallBy || doc.InstalledBy == windowsInstallBy)
}

func receiptInstalledBy(path, goos string) string {
	raw, err := os.ReadFile(path)
	if err == nil {
		var doc struct {
			InstalledBy string `json:"installed_by"`
		}
		if json.Unmarshal(raw, &doc) == nil && doc.InstalledBy != "" {
			return doc.InstalledBy
		}
	}
	if goos == "windows" {
		return windowsInstallBy
	}
	return defaultInstallBy
}

func receiptBody(binary, version, installedBy string) string {
	return fmt.Sprintf("{\"binary\":%q,\"version\":%q,\"installed_by\":%q}\n", binary, version, installedBy)
}

func within(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(canonical(root), canonical(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonical(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(canonical(parent), filepath.Base(path))
}

func canceled(err error) *core.RefusalError {
	return &core.RefusalError{Code: codeCanceled, Message: "canceled before upgrade completed: " + err.Error()}
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
