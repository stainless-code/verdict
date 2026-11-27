// Package agentcatalog owns the capabilities of shipped agents.
package agentcatalog

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// Agent has optional capabilities; absence keeps it out of that verb.
type Agent struct {
	Launch  *Launch
	Skills  *Skills
	MCP     *MCP
	Name    string
	Aliases []string
}

// Skills describes the user skills directory and its environment override.
type Skills struct {
	Base, Env, Citation string
	Parts               []string
	Order               int
}

// MCP describes the config scope, path, and renderer.
type MCP struct {
	Scope, Path, Format, Citation string
	PrintOnly                     bool
}

// Launch describes one non-interactive invocation.
type Launch struct {
	Usage          *Usage
	KeyEnvCitation string
	Citation       string
	Name           string
	Binary         string
	Install        string
	ModelFlag      string
	// ModelAliasCitation is the doc that names ModelFlagAliases.
	ModelAliasCitation string
	PromptFlag         string
	Source             string
	PermissionHint     string
	KeyEnv             string
	PermissionCitation string
	LaunchWarning      string
	ProbeMatch         string
	DiscoveryCitation  string
	ResumeCitation     string
	// EditCitation is the doc that states EditArgs.
	EditCitation string
	// ReviewWriteCitation is the doc that states ReviewWriteArgs' rule syntax.
	ReviewWriteCitation string
	// SnapshotWriteCitation is the doc that states SnapshotWriteArgs.
	SnapshotWriteCitation string
	// ReviewArgsCitation is the doc that states ReviewArgs and
	// ReviewOwnedFlags.
	ReviewArgsCitation string
	ProbeArgs          []string
	DiscoveryArgs      []string
	// DiscoveryOwnedFlags are the spellings of the flag the restriction
	// itself uses (DiscoveryArgs is that flag and its value). A later
	// spelling may replace verdict's, and one the agent does not parse drops
	// it, so on a review launch verdict owns the flag: an operator spelling
	// after `--`, in any form, refuses unless --allow-shell is set.
	DiscoveryOwnedFlags []string
	ResumeArgs          []string
	// DiscoveryOverrideFlags are the flags that decide the agent's whole tool
	// set. They never drop the shell denial — only --allow-shell does — but
	// they decide the review write grant's question, so they drop the grant.
	DiscoveryOverrideFlags []string
	// ModelFlagAliases are other documented spellings of ModelFlag (codex's
	// -m); the self-review loop's `--` allowlist accepts them beside it.
	ModelFlagAliases []string
	Args             []string
	// EditArgs is the documented flag set that lets the CLI edit files in its
	// working directory non-interactively, with the narrowest shell scope its
	// docs allow and no permission checks skipped. Only `verdict loop` applies
	// it, to its fix spawn — whose contract is editing the working tree; the
	// loop's verify step runs the tests, so the fix needs no shell where the
	// row can deny it. A row with no equally scoped flag leaves it and
	// EditCitation empty: an approval bypass is never an edit grant.
	EditArgs []string
	// PermissionFlags are the row's documented flags that decide what the
	// agent may write or run (permission modes, sandboxes, allow lists, extra
	// writable directories, approval bypasses). An operator spelling of one
	// after `--` owns the review grant's question, so run drops the grant;
	// loop refuses them outright, since its `--` reaches the review of
	// untrusted content. PermissionCitation names the doc.
	PermissionFlags []string
	// ReviewWriteArgs is the documented grant that lets a review launch write
	// its artifacts without a prompt, scoped to the launch's own output
	// directory: [ReviewDir] in each argument is replaced by that directory as
	// the CLI's rule path. verdict run adds it to every review launch unless
	// the operator's `--` arguments already decide tool permissions (a
	// PermissionFlags or DiscoveryOverrideFlags spelling). A row without an
	// equally scoped option leaves both it and ReviewWriteCitation empty.
	ReviewWriteArgs []string
	// SnapshotWriteArgs is the documented flag set that lets a review launch
	// write in its working directory, for a row whose only write path is
	// that broad. It is sound only because every review launch runs in a
	// disposable snapshot whose expected artifacts alone are copied back:
	// verdict run adds it to real review launches, never to a --print
	// render (that command runs wherever the operator runs it), and drops it
	// like ReviewWriteArgs when the operator's `--` arguments decide
	// permissions.
	SnapshotWriteArgs []string
	// ReviewArgs are the documented flags every review launch carries,
	// --allow-shell or not, and every render prints: they keep the reviewed
	// tree's own agent configuration (settings, hooks, MCP servers) from
	// loading, since that tree is untrusted content.
	ReviewArgs []string
	// ReviewOwnedFlags are the flags that would undo ReviewArgs or point the
	// agent at configuration or directories outside its review snapshot. An
	// operator spelling of one after `--` refuses on a review launch, as a
	// DiscoveryOwnedFlags spelling does, unless --allow-shell is set: then the
	// operator owns the agent's tool set and its configuration.
	ReviewOwnedFlags []string
	// StateWritable are the home-relative paths, in slash form, where the
	// CLI keeps its own data — sessions, logs, caches, its login's token
	// refresh — the only paths besides its review snapshot a review launch in
	// verdict's OS sandbox may write. They may be read even where the
	// credential table hides a path (claude's macOS login keychain). An entry
	// is a directory (created before a Linux launch so bwrap can bind it), or,
	// ending in `*`, a name prefix for files: one file beside its atomic-write
	// siblings. StateCitation names where each was read or observed.
	StateWritable []string
	// StateReadOnly are the CLI's own config and code under the same roots —
	// settings, plugins, extensions, hooks, agent definitions, MCP config —
	// which a later run outside verdict loads: readable in the sandbox, never
	// writable, so a prompt-injected review cannot persist past it. No
	// StateWritable entry may sit inside one or name one as a prefix. One
	// inside a writable directory names a directory, made before every
	// sandboxed launch so the agent cannot make it its own, and stays
	// read-only over it. A `*` segment matches any one directory
	// (`.claude/projects/*/memory`).
	StateReadOnly []string
	// StateDirs are directories under read-only state the CLI makes at
	// startup and fails without; each is made, empty, before every sandboxed
	// launch, and stays read-only.
	StateDirs []string
	// StateEphemeral are the variables every review launch points at a
	// throwaway directory in its wrapper instead of the CLI's real one —
	// for state a later session would trust — each seeded with no-follow
	// copies of the files Seed names below the real one (its login).
	StateEphemeral []EphemeralVar
	// ProjectState is the per-working-directory state the CLI keeps under
	// one root; a sandboxed review launch may write only its own, made
	// before the launch and removed after.
	ProjectState  *ProjectState
	StateCitation string
	// Confinement is how the CLI confines its own review launch without
	// verdict's OS sandbox, "" for not at all. [OwnSandbox]: its
	// SnapshotWriteArgs start the CLI's own OS sandbox, which verdict's would
	// nest — a nested Seatbelt profile fails on macOS (sandbox_apply:
	// Operation not permitted) — so while those args apply its launch skips
	// verdict's wrapper. [OwnWrites]: its shell is denied and its write grant
	// scoped to the bundle. Either way, on a host with no OS sandbox it still
	// runs, warned that its reads are not confined; every other row refuses
	// there unless the operator opts out. ConfinementCitation names the doc.
	Confinement         string
	ConfinementCitation string
	// TempEnv are the variables that move the CLI's own scratch directory
	// off a hardcoded host temp root; every review launch points them at its
	// private TMPDIR, since the sandbox closes the host temp roots.
	TempEnv []string
	// StateEnv are the variables that relocate the state: an absolute value
	// replaces its Root, so every StateWritable entry at or below Root moves
	// under the value instead.
	StateEnv []StateVar
}

// The Confinement values.
const (
	// OwnSandbox is a CLI whose review launch runs its own OS sandbox.
	OwnSandbox = "own-sandbox"
	// OwnWrites is a CLI whose review launch confines its own writes.
	OwnWrites = "own-writes"
)

// StateVar is one environment variable that relocates an agent's state: its
// value stands in for the home-relative Root.
type StateVar struct {
	Name string
	Root string
}

// EphemeralVar is one variable a review launch points at a throwaway
// directory: Root is its home-relative default, Seed the files below the real
// directory copied in first, and SeedKeys, for a JSON seed, the top-level
// keys its copy keeps (every other key dropped).
type EphemeralVar struct {
	SeedKeys map[string][]string
	Name     string
	Root     string
	Seed     []string
}

// ProjectState is per-working-directory state: Root is the home-relative
// directory (relocated like the row's state) holding one directory per
// working directory, named by the working directory's real path with every
// character but an ASCII letter or digit a dash; MaxSlug is the longest name
// the CLI uses as is (a longer one it shortens unpredictably, so the launch
// gets none); ReadOnly are the subdirectories of it a later session loads.
type ProjectState struct {
	Root     string
	ReadOnly []string
	MaxSlug  int
}

// ReviewDir is the placeholder ReviewWriteArgs carries for the launch's
// output directory.
const ReviewDir = "{review-dir}"

// Usage describes one CLI's structured output that carries its run's cost or
// token usage. Args request it (the first one is the flag an operator's own
// spelling after `--` overrides); Format names the parser that reads it from
// the captured stdout; Citation is the doc that states the output shape.
type Usage struct {
	Format   string
	Citation string
	Args     []string
}

// The structured-output formats verdict parses. Each names one CLI's own
// documented shape; a parse that does not match it measures nothing.
const (
	// UsageClaudeJSON is `claude -p --output-format json`: one result object
	// carrying total_cost_usd and the final text in result.
	UsageClaudeJSON = "claude-json"
	// UsageCodexJSONL is `codex exec --json`: a JSONL event stream whose
	// turn.completed events carry token usage, priced from the price table.
	UsageCodexJSONL = "codex-jsonl"
	// UsageOpenCodeJSONL is `opencode run --format json`: a JSONL event stream
	// whose step_finish events carry each step's cost.
	UsageOpenCodeJSONL = "opencode-jsonl"
)

// modelFlag is the argument every shipped row takes a model name on; a row
// whose binary spells it differently carries its own value.
const modelFlag = "--model"

// The docs each row cites for its CLI facts, shared by the launch citation and
// the permission or continuation claim beside it.
const (
	docBobShell  = "https://bob.ibm.com/docs/shell/getting-started/start-bobshell-non-interactive"
	docClaudeCLI = "https://code.claude.com/docs/en/cli-reference.md"
	// docClaudePermissions states the Edit rule syntax: Edit rules cover every
	// built-in file-editing tool, `./path` is relative to the working
	// directory, `//path` is absolute (a Windows path in its /c/... form),
	// and `**` crosses directories — read 2026-11-26.
	docClaudePermissions = "https://code.claude.com/docs/en/permissions"
	// docClaudeHeadless states the json output's result object, including
	// total_cost_usd and the final text in result — read 2026-11-25.
	docClaudeHeadless = "https://code.claude.com/docs/en/headless"
	// docCodexExecJSON states `codex exec --json` and its event stream;
	// codex-rs/exec/src/exec_events.rs holds turn.completed's usage struct —
	// read 2026-11-25.
	docCodexExecJSON = "https://learn.chatgpt.com/docs/non-interactive-mode"
	// docOpenCodeRun states `opencode run --format json`; the event shape
	// (step_finish's part.cost and tokens, text deltas) is in
	// anomalyco/opencode packages/cli/src/run/noninteractive.ts — read
	// 2026-11-25.
	docOpenCodeRun = "https://opencode.ai/docs/cli/commands"
	docCodexReadme = "https://github.com/openai/codex/blob/main/README.md"
	// docCodexConfig documents the sandbox_workspace_write table
	// (writable_roots, exclude_tmpdir_env_var, exclude_slash_tmp).
	docCodexConfig = "https://github.com/openai/codex/blob/main/docs/config.md"
	// docCodexCLIRef lists codex's and codex exec's flags, `--model, -m`
	// among them — read 2026-11-27.
	docCodexCLIRef  = "https://learn.chatgpt.com/docs/developer-commands?surface=cli"
	docOpenCodeCLI  = "https://opencode.ai/docs/cli/"
	docPiCLI        = "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/cli.md"
	docPiQuickstart = "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/quickstart.md"
	// docClaudeSettings names ~/.claude/settings.json and ~/.claude.json,
	// and CLAUDE_CONFIG_DIR — read 2026-11-27.
	docClaudeSettings = "https://code.claude.com/docs/en/settings"
	// docClaudeIAM states that macOS keeps Claude Code's credentials in the
	// encrypted macOS Keychain — read 2026-11-27.
	docClaudeIAM = "https://code.claude.com/docs/en/iam"
	// docCodexSandbox states workspace-write's Seatbelt and Landlock
	// sandboxes and what they let the agent write — read 2026-11-27.
	docCodexSandbox = "https://developers.openai.com/codex/security"
	// docOpenCodeStorage names opencode's data directory
	// (~/.local/share/opencode: auth, sessions, logs), its config directory
	// and its cache (~/.cache/opencode) — read 2026-11-27.
	docOpenCodeStorage = "https://opencode.ai/docs/troubleshooting/"
	// docPiConfig is getAgentDir: PI_CODING_AGENT_DIR, else ~/.pi/agent —
	// read 2026-11-27.
	docPiConfig = "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/src/config.ts"
)

const (
	nameClaude   = "claude"
	nameCodex    = "codex"
	nameOpenCode = "opencode"
	argsRun      = "run"
	argsExec     = "exec"
	argsAsk      = "ask"
	argsHelp     = "--help"
)

// flagDisableToolGroups is Bob's tool-group switch, spelled once for the row's
// probe match, its discovery restriction, and the operator spelling that
// replaces it.
const flagDisableToolGroups = "--disable-tool-groups"

// flagPermissionMode is claude's permission-mode flag, spelled once for its
// edit grant, its permission flags, and the mode a review pins.
const flagPermissionMode = "--permission-mode"

// flagSandbox and valueWorkspaceWrite are codex's sandbox flag and the mode
// that writes its working directory.
const (
	flagSandbox         = "--sandbox"
	valueWorkspaceWrite = "workspace-write"
)

// agyLaunchWarning flags Antigravity CLI's headless write soft-denial before
// launch: outside trustedWorkspaces, `agy -p` auto-denies file edits and exits
// 0 with no artifact, while approval flags stay with the operator after `--`.
const agyLaunchWarning = "agy print mode (-p) soft-denies file edits outside trusted workspaces, " +
	"so review.json may never be written; trust this repository in agy's settings or pass " +
	"-- --dangerously-skip-permissions (your approval bypass)"

const valueHome = "home"

const valueSkills = "skills"

const valueCwd = "cwd"

const valueJSON = "json"

const valueGoose = "goose"

var catalog = []Agent{
	// --agent 'adal' is Sylph's product slug.
	// https://docs.sylph.ai/integration/headless.md — read 2026-11-17
	{
		Name: "adal",
		Launch: &Launch{
			Citation: "https://docs.sylph.ai/integration/headless.md",
			Binary:   "adal", ModelFlag: modelFlag, PromptFlag: "-q",
			Install: "curl -fsSL https://adal.sylph.ai/install.sh | bash",
		},
	},
	// https://github.com/google-antigravity/antigravity-cli — read 2026-11-17
	{
		Name: "agy",
		Launch: &Launch{
			Citation:           "https://github.com/google-antigravity/antigravity-cli",
			PermissionHint:     "--dangerously-skip-permissions",
			PermissionCitation: "https://github.com/google-antigravity/antigravity-cli",
			Binary:             "agy", ModelFlag: modelFlag, PromptFlag: "-p",
			LaunchWarning: agyLaunchWarning,
			Install:       "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		},
	},
	// https://ampcode.com/docs/markdown/cli/execute-mode — read 2026-11-17
	{
		Name: "amp",
		Launch: &Launch{
			Citation: "https://ampcode.com/docs/markdown/cli/execute-mode",
			Binary:   "amp", ModelFlag: modelFlag, PromptFlag: "-x",
			Install: "npm install -g @ampcode/cli",
		},
	},
	// --agent 'auggie' is the CLI name for Augment.
	// https://docs.augmentcode.com/cli/reference.md — read 2026-11-17
	{
		Name: "auggie",
		Launch: &Launch{
			Citation: "https://docs.augmentcode.com/cli/reference.md",
			Binary:   "auggie", ModelFlag: modelFlag,
			Args:    []string{"--print"},
			Install: "npm install -g @augmentcode/auggie@prerelease",
		},
	},
	// --agent 'autohand' is the CLI name for Autohand Code.
	// https://github.com/autohandai/code-cli/blob/main/src/index.ts — read 2026-11-17
	{
		Name: "autohand",
		Launch: &Launch{
			Citation: "https://github.com/autohandai/code-cli/blob/main/src/index.ts",
			Binary:   "autohand", ModelFlag: modelFlag,
			Install: "curl -fsSL https://autohand.ai/install.sh | bash",
		},
	},
	// brew info bob: neovim version manager; brew search --formula /^bob$/ -> bob (clash).
	// `--disable-tool-groups <groups>` "Disable specific tool groups (comma-separated, for example execute,mcp)"
	// https://bob.ibm.com/docs/shell/getting-started/start-bobshell-non-interactive — read 2026-11-17
	{
		Name: "bob",
		Launch: &Launch{
			Citation:  docBobShell,
			Binary:    "bob",
			Args:      []string{argsRun},
			ProbeArgs: []string{argsRun, argsHelp}, ProbeMatch: flagDisableToolGroups,
			// The execute group is the shell; the write path stays open.
			DiscoveryArgs:       []string{flagDisableToolGroups, "execute,mcp"},
			DiscoveryCitation:   docBobShell,
			DiscoveryOwnedFlags: []string{flagDisableToolGroups},
			Install:             "curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash",
		},
	},
	// https://code.claude.com/docs/en/cli-reference.md — read 2026-11-17
	{
		Name: "claude",
		Launch: &Launch{
			Citation:           docClaudeCLI,
			PermissionHint:     "--permission-mode acceptEdits",
			PermissionCitation: docClaudeCLI,
			// acceptEdits also auto-approves filesystem shell commands (mkdir,
			// rm, mv, sed), so the fix keeps the review's shell denial.
			EditArgs:     []string{flagPermissionMode, "acceptEdits", "--disallowedTools=Bash,PowerShell"},
			EditCitation: docClaudePermissions,
			PermissionFlags: []string{
				flagPermissionMode, "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
				"--allowedTools", "--allowed-tools", "--add-dir",
			},
			// The joined `=` form, like the discovery restriction: the
			// separated form would swallow the directive as a rule.
			ReviewWriteArgs:     []string{"--allowedTools=Edit(" + ReviewDir + "/**)"},
			ReviewWriteCitation: docClaudePermissions,
			KeyEnv:              "ANTHROPIC_API_KEY",
			KeyEnvCitation:      docClaudeCLI,
			Binary:              nameClaude, ModelFlag: modelFlag,
			Args:           []string{"-p"},
			ResumeArgs:     []string{"-c"},
			ResumeCitation: docClaudeCLI,
			// `--disallowedTools` takes `<tools...>`, so the joined `=` form is
			// the one that cannot swallow the directive as a tool name. Bash and
			// PowerShell are both denied — a Windows Claude Code reaches the
			// shell through either — which leaves Read/Glob/Grep/Write for
			// review.json.
			DiscoveryArgs:     []string{"--disallowedTools=Bash,PowerShell"},
			DiscoveryCitation: docClaudeCLI,
			// `--setting-sources=user` loads the operator's own settings only:
			// the reviewed tree's .claude/settings*.json can carry hooks (shell
			// commands) and allow rules for paths outside the review snapshot.
			// `--strict-mcp-config` loads no MCP server but --mcp-config's, so
			// the tree's .mcp.json starts none. The flags that would undo them
			// or load other configuration are verdict's on a review launch.
			// `--permission-mode default` outranks a settings defaultMode
			// (acceptEdits, bypassPermissions) that would widen its writes —
			// verified empirically against the agent's CLI.
			ReviewArgs:         []string{"--setting-sources=user", "--strict-mcp-config", flagPermissionMode, "default"},
			ReviewArgsCitation: docClaudeCLI,
			ReviewOwnedFlags: []string{
				"--settings", "--setting-sources", "--plugin-dir", "--mcp-config", "--strict-mcp-config",
			},
			// The shell denial is never dropped for an operator flag; a deny rule
			// beats any allow. --tools decides the tool set, so it drops the
			// review write grant.
			DiscoveryOverrideFlags: []string{"--tools"},
			Usage: &Usage{
				Format: UsageClaudeJSON, Citation: docClaudeHeadless,
				Args: []string{"--output-format", "json"},
			},
			Install: "curl -fsSL https://claude.ai/install.sh | bash",
			// ~/.claude holds settings, plugins, skills, agents, commands,
			// hooks and output styles beside its session data, and
			// ~/.claude.json holds mcpServers beside its counters, per the
			// settings doc; CLAUDE_CONFIG_DIR relocates ~/.claude. The macOS
			// login lives in the login keychain, which the Security framework
			// reads and writes in-process (token refresh). The data
			// directories and ~/Library/Caches/claude-cli-nodejs, and that it
			// reaches its API with nothing under ~/.claude writable but its
			// debug log and its launch's project directory, were verified
			// empirically against the agent's CLI.
			// Read-only, because a later session loads them as instructions
			// or code: shell-snapshots and session-env (sourced scripts),
			// sessions, todos and file-history (a resumed session's state and
			// the files /rewind restores), backups (a corrupt ~/.claude.json
			// is restored from them), cache and statsig (cached configuration
			// and feature gates). .credentials.json is the Linux login.
			StateWritable: []string{
				".claude/debug", ".claude/.credentials.json*", "Library/Caches/claude-cli-nodejs", "Library/Keychains",
			},
			StateReadOnly: []string{
				".claude.json", ".claude/settings.json", ".claude/settings.local.json", ".claude/plugins",
				".claude/skills", ".claude/agents", ".claude/commands", ".claude/hooks", ".claude/output-styles",
				".claude/CLAUDE.md", ".claude/shell-snapshots", ".claude/session-env", ".claude/sessions",
				".claude/todos", ".claude/file-history", ".claude/backups", ".claude/cache", ".claude/statsig",
			},
			// Its transcript goes to projects/<slug of the working directory's
			// real path, every non-alphanumeric character a dash>, a longer
			// slug than 200 characters ending in a hash it alone computes;
			// memory there holds instructions every later session there
			// loads — verified empirically against the agent's CLI.
			ProjectState: &ProjectState{Root: ".claude/projects", ReadOnly: []string{"memory"}, MaxSlug: 200},
			StateEnv:     []StateVar{{Name: "CLAUDE_CONFIG_DIR", Root: ".claude"}},
			// claude makes /tmp/claude-<uid> whatever TMPDIR says;
			// CLAUDE_CODE_TMPDIR moves it (named in its own disk-quota
			// message) — verified empirically against the agent's CLI.
			TempEnv:       []string{"CLAUDE_CODE_TMPDIR"},
			StateCitation: docClaudeSettings + " ; " + docClaudeIAM,
			// Bash and PowerShell denied (DiscoveryArgs) and Edit allowed only
			// under the bundle (ReviewWriteArgs): Write/Edit outside an allow
			// rule are denied in print mode.
			Confinement:         OwnWrites,
			ConfinementCitation: docClaudePermissions,
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "CLAUDE_CONFIG_DIR",
			Parts:    []string{".claude", valueSkills},
			Order:    1,
			Citation: "https://code.claude.com/docs/en/skills",
		},
		MCP: &MCP{
			Scope:     valueCwd,
			Path:      ".mcp.json",
			Format:    valueJSON,
			PrintOnly: false,
			Citation:  "https://code.claude.com/docs/en/mcp",
		},
	},
	// One-shot: `cline "your prompt"` runs a single turn in yolo mode and exits.
	// https://github.com/cline/cline/blob/main/apps/cli/README.md — read 2026-11-17
	{
		Name: "cline",
		Launch: &Launch{
			Citation: "https://github.com/cline/cline/blob/main/apps/cli/README.md",
			Binary:   "cline", ModelFlag: modelFlag,
			Install: "npm install -g cline",
		},
	},
	// --agent 'codearts' is the CLI name for CodeArts Agent.
	// https://support.huaweicloud.com/usermanual-cli/codeartsagent_cli_0034.html — read 2026-11-17
	{
		Name: "codearts",
		Launch: &Launch{
			Citation: "https://support.huaweicloud.com/usermanual-cli/codeartsagent_cli_0034.html",
			Binary:   "codearts", ModelFlag: modelFlag,
			Args: []string{argsRun},
			Install: "sh -c \"$(curl -L https://cnnorth4-cloudide-marketplace.obs.cn-north-4.myhuaweicloud.com/" +
				"codearts/cli_tui/install_script/install.sh)\"",
		},
	},
	// https://www.codebuddy.ai/docs/cli/headless — read 2026-11-17
	{
		Name: "codebuddy",
		Launch: &Launch{
			Citation: "https://www.codebuddy.ai/docs/cli/headless",
			Binary:   "codebuddy", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "npm install -g @tencent-ai/codebuddy-code",
		},
	},
	// https://github.com/openai/codex/blob/main/README.md — read 2026-11-17
	{
		Name: "codex",
		Launch: &Launch{
			Citation:           docCodexReadme,
			PermissionHint:     "--sandbox workspace-write",
			PermissionCitation: docCodexReadme,
			EditArgs:           []string{flagSandbox, valueWorkspaceWrite},
			EditCitation:       docCodexReadme,
			// `codex exec` defaults to the read-only sandbox, so a review could
			// only hand its review.json back through stdout salvage; inside the
			// review snapshot the workspace sandbox writes the copy, and only
			// the expected artifacts come back. Its writable roots also hold
			// /tmp unless excluded; TMPDIR is the review's private one.
			SnapshotWriteArgs: []string{
				flagSandbox, valueWorkspaceWrite, "-c", "sandbox_workspace_write.exclude_slash_tmp=true",
				// A writable root in the operator's config.toml would widen the
				// sandbox past the snapshot; the override empties the list.
				"-c", "sandbox_workspace_write.writable_roots=[]",
			},
			SnapshotWriteCitation: docCodexConfig,
			// A working directory, a managed worktree, an approval reviewer,
			// or trusted repository hooks would each take the review out of
			// its snapshot or run the tree's own code.
			// --ephemeral persists no session: verdict resumes none, and a
			// later `codex resume` would load one as its context.
			ReviewArgs: []string{"--ephemeral"},
			ReviewOwnedFlags: []string{
				"-C", "--cd", "--worktree", "--approve-for-me", "--dangerously-bypass-hook-trust", "--ephemeral",
				"--ignore-user-config",
			},
			ReviewArgsCitation: docCodexCLIRef,
			// `--model, -m` per the codex CLI reference.
			ModelFlagAliases:   []string{"-m"},
			ModelAliasCitation: docCodexCLIRef,
			// workspace-write also runs shell commands, sandboxed to the
			// workspace; codex documents no edit-only mode.
			PermissionFlags: []string{
				flagSandbox, "-s", "--ask-for-approval", "-a", "--full-auto",
				"--dangerously-bypass-approvals-and-sandbox", "--add-dir",
				// A config override or profile can set sandbox_mode itself, a
				// feature toggle is a config override, --remote runs the agent
				// in an app server outside any sandbox verdict starts, --trust
				// persists trust (MCP commands outside the sandbox included),
				// and -o writes a file wherever it names.
				"-c", "--config", "-p", "--profile", "--enable", "--disable", "--remote",
				"--remote-auth-token-env", "--trust", "-o", "--output-last-message",
			},
			KeyEnv:         "OPENAI_API_KEY",
			KeyEnvCitation: docCodexReadme,
			Binary:         nameCodex, ModelFlag: modelFlag,
			Args:           []string{argsExec},
			ResumeArgs:     []string{"resume"},
			ResumeCitation: docCodexReadme,
			Usage:          &Usage{Format: UsageCodexJSONL, Citation: docCodexExecJSON, Args: []string{"--json"}},
			Install:        "npm install -g @openai/codex",
			// CODEX_HOME, ~/.codex by default, holds config.toml, AGENTS.md,
			// rules, prompts, skills and plugins beside auth.json, sessions,
			// history and logs — and its cache (MCP tool schemas, the plugin
			// catalog), scratch (plugin and git staging) and tmp/arg0* (the
			// PATH shims of running sessions), which later sessions load or
			// run. So every review launch, its own sandbox or verdict's, gets
			// a throwaway CODEX_HOME in its wrapper seeded with its login and
			// config, and the real one is read-only; with --ephemeral it
			// starts and reaches its API there — verified empirically against
			// the agent's CLI.
			StateReadOnly: []string{dirCodex},
			StateEphemeral: []EphemeralVar{
				{
					Name: envCodexHome, Root: dirCodex, Seed: []string{"auth.json", fileCodexConfig},
					// Model, provider, and connection keys alone: an MCP server,
					// a notify or hook command, a profile, a feature, project
					// trust, or a sandbox setting would start or widen something
					// outside every sandbox (codex merges `-c` overrides, so
					// none can clear them).
					SeedKeys: map[string][]string{fileCodexConfig: {
						"model", "model_provider", "model_reasoning_effort", "model_reasoning_summary",
						"model_verbosity", "model_context_window", "model_max_output_tokens",
						"forced_login_method", "chatgpt_base_url",
						"model_providers.*.name", "model_providers.*.base_url", "model_providers.*.env_key",
						"model_providers.*.env_key_instructions", "model_providers.*.wire_api",
						"model_providers.*.query_params", "model_providers.*.http_headers",
						"model_providers.*.env_http_headers", "model_providers.*.request_max_retries",
						"model_providers.*.stream_max_retries", "model_providers.*.stream_idle_timeout_ms",
						"model_providers.*.requires_openai_auth",
					}},
				},
			},
			StateEnv:      []StateVar{{Name: envCodexHome, Root: dirCodex}},
			StateCitation: docCodexReadme,
			// workspace-write runs codex's own Seatbelt (macOS) or
			// Landlock/seccomp (Linux) sandbox, which confines its writes —
			// shell included — to the working directory and temp roots.
			Confinement:         OwnSandbox,
			ConfinementCitation: docCodexSandbox,
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      envCodexHome,
			Parts:    []string{dirCodex, valueSkills},
			Order:    2,
			Citation: "https://developers.openai.com/codex/skills",
		},
		MCP: &MCP{
			Scope:     "codex",
			Path:      "config.toml",
			Format:    "toml",
			PrintOnly: false,
			Citation:  "https://developers.openai.com/codex/mcp",
		},
	},
	// https://commandcode.ai/docs/headless — read 2026-11-17
	{
		Name: "commandcode",
		Launch: &Launch{
			Citation: "https://commandcode.ai/docs/headless",
			Binary:   "commandcode", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "npm i -g command-code@latest",
		},
	},
	// --agent 'continue' is Continue's product slug; the binary is cn.
	// https://github.com/continuedev/continue/blob/main/docs/cli/quickstart.mdx — read 2026-11-17
	{
		Name: "continue",
		Launch: &Launch{
			Citation: "https://github.com/continuedev/continue/blob/main/docs/cli/quickstart.mdx",
			Binary:   "cn", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "npm i -g @continuedev/cli",
		},
	},
	// https://docs.github.com/en/copilot/how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically — read 2026-11-17
	{
		Name: "copilot",
		Launch: &Launch{
			Citation: "https://docs.github.com/en/copilot/how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically",
			Binary:   "copilot", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "npm install -g @github/copilot",
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".copilot", valueSkills},
			Order:    5,
			Citation: "https://docs.github.com/en/copilot/concepts/agents/about-agent-skills",
		},
		MCP: &MCP{
			Scope:     valueCwd,
			Path:      ".vscode/mcp.json",
			Format:    valueJSON,
			PrintOnly: true,
			Citation:  "https://code.visualstudio.com/docs/copilot/customization/mcp-servers",
		},
	},
	// brew info cortex: Prometheus long-term storage; brew search --formula /^cortex$/ -> cortex (clash).
	// `--bypass, --dangerously-allow-all-tool-calls` "Enable bypass mode, automatically approving all tool calls"
	// https://docs.snowflake.com/en/user-guide/cortex-code/cli-reference — read 2026-11-17
	{
		Name: "cortex",
		Launch: &Launch{
			Citation: "https://docs.snowflake.com/en/user-guide/cortex-code/cli-reference",
			Binary:   "cortex", ModelFlag: modelFlag, PromptFlag: "-p",
			ProbeArgs: []string{argsHelp}, ProbeMatch: "--dangerously-allow-all-tool-calls",
			Install: "curl -qLsS https://ai.snowflake.com/static/cc-scripts/install.sh | sh",
		},
	},
	// https://github.com/charmbracelet/crush/blob/main/internal/cmd/run.go — read 2026-11-17
	{
		Name: "crush",
		Launch: &Launch{
			Citation: "https://github.com/charmbracelet/crush/blob/main/internal/cmd/run.go",
			Binary:   "crush", ModelFlag: modelFlag,
			Args:    []string{argsRun},
			Install: "brew install charmbracelet/tap/crush",
		},
	},
	// https://cursor.com/docs/cli/headless — read 2026-11-17
	{
		Name: "cursor",
		Launch: &Launch{
			Citation: "https://cursor.com/docs/cli/headless",
			Binary:   "cursor-agent", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "curl https://cursor.com/install -fsS | bash",
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".cursor", valueSkills},
			Order:    3,
			Citation: "https://cursor.com/docs/context/skills",
		},
		MCP: &MCP{
			Scope:     valueCwd,
			Path:      ".cursor/mcp.json",
			Format:    valueJSON,
			PrintOnly: false,
			Citation:  "https://cursor.com/docs/context/mcp",
		},
	},
	// --agent 'dcode' is the CLI name for Deep Agents.
	// https://github.com/langchain-ai/deepagents/blob/main/libs/code/deepagents_code/main.py — read 2026-11-17
	{
		Name: "dcode",
		Launch: &Launch{
			Citation: "https://github.com/langchain-ai/deepagents/blob/main/libs/code/deepagents_code/main.py",
			Binary:   "dcode", ModelFlag: modelFlag, PromptFlag: "-n",
			Install: "curl -LsSf https://langch.in/dcode | bash",
		},
	},
	// --agent 'devin' is the product slug for Devin for Terminal.
	// https://docs.devin.ai/cli/reference/commands.md — read 2026-11-17
	{
		Name: "devin",
		Launch: &Launch{
			Citation: "https://docs.devin.ai/cli/reference/commands.md",
			Binary:   "devin", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "curl -fsSL https://cli.devin.ai/install.sh | bash",
		},
	},
	// https://github.com/truffle-ai/dexto/blob/main/packages/cli/src/cli/commands/run/register.ts — read 2026-11-17
	{
		Name: "dexto",
		Launch: &Launch{
			Citation: "https://github.com/truffle-ai/dexto/blob/main/packages/cli/src/cli/commands/run/register.ts",
			Binary:   "dexto", ModelFlag: modelFlag,
			Args:    []string{argsRun},
			Install: "npm install -g dexto",
		},
	},
	// https://docs.factory.com/droid-cli/cli-reference.md — read 2026-11-17
	{
		Name: "droid",
		Launch: &Launch{
			Citation: "https://docs.factory.com/droid-cli/cli-reference.md",
			Binary:   "droid", ModelFlag: modelFlag,
			Args:    []string{argsExec},
			Install: "curl -fsSL https://app.factory.ai/cli | sh",
		},
	},
	// brew info forge: ArrayFire visualization; brew search --formula /^forge$/ -> forge (clash).
	// `#[arg(long, alias = "cid")] pub conversation_id: Option<ConversationId>,`
	// https://github.com/tailcallhq/forgecode/blob/main/crates/forge_main/src/cli.rs — read 2026-11-17
	{
		Name: "forge",
		Launch: &Launch{
			Citation: "https://github.com/tailcallhq/forgecode/blob/main/crates/forge_main/src/cli.rs",
			Binary:   "forge", PromptFlag: "-p",
			ProbeArgs: []string{argsHelp}, ProbeMatch: "--conversation-id",
			Install: "curl -fsSL https://forgecode.dev/cli | sh",
		},
	},
	// brew info fx: terminal JSON viewer; brew search --formula /^fx$/ -> fx (clash).
	// `fx ask` documents `--model`, `--effort`, `--fast`, `--ultrafast` per request
	// https://fx.sh/docs/using-fx/fx-ask — read 2026-11-17
	{
		Name: "fx",
		Launch: &Launch{
			Citation: "https://fx.sh/docs/using-fx/fx-ask",
			Binary:   "fx", ModelFlag: modelFlag,
			Args:      []string{argsAsk},
			ProbeArgs: []string{argsAsk, argsHelp}, ProbeMatch: "--ultrafast",
			Install: "curl -fsSL https://fx.sh/setup.sh | bash",
		},
	},
	// https://github.com/google-gemini/gemini-cli/blob/main/README.md — read 2026-11-17
	{
		Name: "gemini",
		Launch: &Launch{
			Citation: "https://github.com/google-gemini/gemini-cli/blob/main/README.md",
			Binary:   "gemini", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "npm install -g @google/gemini-cli",
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".gemini", valueSkills},
			Order:    6,
			Citation: "https://geminicli.com/docs/cli/skills/",
		},
		MCP: &MCP{
			Scope:     valueHome,
			Path:      ".gemini/settings.json",
			Format:    valueJSON,
			PrintOnly: false,
			Citation:  "https://geminicli.com/docs/tools/mcp-server/",
		},
	},
	// https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/running-tasks.md — read 2026-11-17
	{
		Name: valueGoose,
		Launch: &Launch{
			Citation: "https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/running-tasks.md",
			Binary:   valueGoose, ModelFlag: modelFlag, PromptFlag: "-t",
			Args:    []string{argsRun},
			Install: "brew install block-goose-cli",
		},
		Skills: &Skills{
			Base:     "config",
			Env:      "",
			Parts:    []string{valueGoose, valueSkills},
			Order:    7,
			Citation: "https://block.github.io/goose/docs/guides/context-engineering/using-skills/",
		},
		MCP: &MCP{
			Scope:     valueHome,
			Path:      ".config/goose/config.yaml",
			Format:    "yaml",
			PrintOnly: true,
			Citation:  "https://block.github.io/goose/docs/getting-started/using-extensions/",
		},
	},
	// brew info grok: regex tool; brew search --formula /^grok$/ -> grok (clash).
	// headless flags list `--prompt-json <JSON>`
	// https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/14-headless-mode.md — read 2026-11-17
	{
		Name: "grok",
		Launch: &Launch{
			Citation: "https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/14-headless-mode.md",
			Binary:   "grok", ModelFlag: modelFlag, PromptFlag: "-p",
			ProbeArgs: []string{argsHelp}, ProbeMatch: "--prompt-json",
			Install: "curl -fsSL https://x.ai/cli/install.sh | bash",
		},
	},
	// --agent 'hermes' is the CLI name for Hermes Agent.
	// https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/_parser.py — read 2026-11-17
	{
		Name: "hermes",
		Launch: &Launch{
			Citation: "https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/_parser.py",
			Binary:   "hermes", ModelFlag: modelFlag, PromptFlag: "-z",
			Install: "curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash",
		},
	},
	// brew info jazz: no formula; brew search --formula /^jazz$/ -> none (no known clash, no probe).
	// jazz run needs --agent <id>; pass it after '--'.
	// https://github.com/lvndry/jazz/blob/main/docs/commands.md — read 2026-11-17
	{
		Name: "jazz",
		Launch: &Launch{
			Citation: "https://github.com/lvndry/jazz/blob/main/docs/commands.md",
			Binary:   "jazz",
			Args:     []string{argsRun},
			Install:  "curl -fsSL https://github.com/lvndry/jazz/releases/latest/download/install.sh | bash",
		},
	},
	// --model is confirmed in the Junie CLI reference (parameters.html, read 2026-11-17).
	// https://junie.jetbrains.com/docs/junie-headless.html — read 2026-11-17
	{
		Name: "junie",
		Launch: &Launch{
			Citation: "https://junie.jetbrains.com/docs/junie-headless.html",
			Binary:   "junie", ModelFlag: modelFlag,
			Install: "curl -fsSL https://junie.jetbrains.com/install.sh | bash",
		},
	},
	// brew info kilo: no formula; brew search --formula /^kilo$/ -> none; antirez/kilo is a text editor (clash).
	// `.option("cloud-fork", { … describe: "fetch session from cloud and continue locally (use with --session)"`
	// https://github.com/Kilo-Org/kilocode/blob/main/packages/opencode/src/cli/cmd/run.ts — read 2026-11-17
	{
		Name: "kilo",
		Launch: &Launch{
			Citation: "https://github.com/Kilo-Org/kilocode/blob/main/packages/opencode/src/cli/cmd/run.ts",
			Binary:   "kilo", ModelFlag: modelFlag,
			Args:      []string{argsRun},
			ProbeArgs: []string{argsRun, argsHelp}, ProbeMatch: "--cloud-fork",
			Install: "npm install -g @kilocode/cli",
		},
	},
	// --agent 'kimchi' is the product slug.
	// https://docs.kimchi.dev/docs/coding-headless.md — read 2026-11-17
	{
		Name: "kimchi",
		Launch: &Launch{
			Citation: "https://docs.kimchi.dev/docs/coding-headless.md",
			Binary:   "kimchi", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "curl -fsSL https://github.com/getkimchi/kimchi/releases/latest/download/install.sh | bash",
		},
	},
	// --agent 'kimi' is the CLI name for Kimi Code.
	// https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/reference/kimi-command.md — read 2026-11-17
	{
		Name: "kimi",
		Launch: &Launch{
			Citation: "https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/reference/kimi-command.md",
			Binary:   "kimi", ModelFlag: modelFlag, PromptFlag: "-p",
			Install: "curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash",
		},
	},
	// https://kiro.dev/docs/cli/headless.md — read 2026-11-17
	{
		Name: "kiro",
		Launch: &Launch{
			Citation: "https://kiro.dev/docs/cli/headless.md",
			Binary:   "kiro-cli", ModelFlag: modelFlag,
			Args:    []string{"chat", "--no-interactive"},
			Install: "curl -fsSL https://cli.kiro.dev/install | bash",
		},
	},
	// --agent 'kode' is the product slug.
	// https://github.com/shareAI-lab/Kode-CLI/blob/main/apps/cli/src/entrypoints/cli/cliParser/program.tsx — read 2026-11-17
	{
		Name: "kode",
		Launch: &Launch{
			Citation: "https://github.com/shareAI-lab/Kode-CLI/blob/main/apps/cli/src/entrypoints/cli/cliParser/program.tsx",
			Binary:   "kode", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "npm install -g @shareai-lab/kode",
		},
	},
	// --agent 'mcode' is the CLI name for MiniMax Code.
	// https://github.com/MiniMax-AI/minimax-code/blob/main/packages/tui/src/cli/contract.ts — read 2026-11-17
	{
		Name: "mcode",
		Launch: &Launch{
			Citation: "https://github.com/MiniMax-AI/minimax-code/blob/main/packages/tui/src/cli/contract.ts",
			Binary:   "mcode", ModelFlag: modelFlag,
			Args:    []string{argsExec},
			Install: "curl -fsSL https://filecdn.minimax.chat/public/install.sh | bash",
		},
	},
	// https://github.com/neovateai/neovate-code/blob/master/src/index.ts — read 2026-11-17
	{
		Name: "neovate",
		Launch: &Launch{
			Citation: "https://github.com/neovateai/neovate-code/blob/master/src/index.ts",
			Binary:   "neovate", ModelFlag: modelFlag,
			Args:    []string{"-q"},
			Install: "npm install -g @neovate/code",
		},
	},
	// --agent 'openclaw' is the product slug.
	// https://github.com/openclaw/openclaw/blob/main/docs/cli/agent.md — read 2026-11-17
	{
		Name: "openclaw",
		Launch: &Launch{
			Citation: "https://github.com/openclaw/openclaw/blob/main/docs/cli/agent.md",
			Binary:   "openclaw", ModelFlag: modelFlag,
			Args:    []string{"agent", argsExec},
			Install: "curl -fsSL https://openclaw.ai/install.sh | bash",
		},
	},
	// https://opencode.ai/docs/cli/ — read 2026-11-17
	{
		Name: "opencode",
		Launch: &Launch{
			Citation: docOpenCodeCLI,
			Binary:   nameOpenCode, ModelFlag: modelFlag,
			Args:           []string{argsRun},
			ResumeArgs:     []string{"-c", "-s", "--fork"},
			ResumeCitation: docOpenCodeCLI,
			Usage: &Usage{
				Format: UsageOpenCodeJSONL, Citation: docOpenCodeRun,
				Args: []string{"--format", "json"},
			},
			Install: "npm install -g @opencode/cli",
			// The XDG data directory holds auth, sessions (opencode.db),
			// logs, snapshots and tool output; the config directory holds
			// opencode.json, plugins, agents, commands and skills, and the
			// cache directory the provider and plugin packages it installs.
			// ~/.local/state/opencode (model, kv, locks, prompt history) was
			// verified empirically against the agent's CLI.
			// Its data directory also holds what a later session trusts — the
			// shadow git repositories of snapshot and repos (a planted
			// core.fsmonitor runs on the next session), managed tools, LSP
			// binaries and packages — so a review launch gets a throwaway data
			// and state home in its wrapper, seeded with copies of its logins,
			// and the real ones stay read-only. It makes the cache's bin at
			// startup. Verified empirically against the agent's CLI.
			StateReadOnly: []string{
				".config/opencode", ".cache/opencode", ".local/share/opencode", ".local/state/opencode",
			},
			StateDirs: []string{".cache/opencode/bin"},
			StateEphemeral: []EphemeralVar{
				{Name: "XDG_DATA_HOME", Root: ".local/share", Seed: []string{"opencode/auth.json", "opencode/mcp-auth.json"}},
				{Name: "XDG_STATE_HOME", Root: ".local/state"},
			},
			StateEnv: []StateVar{
				{Name: "XDG_DATA_HOME", Root: ".local/share"},
				{Name: "XDG_STATE_HOME", Root: ".local/state"},
				{Name: "XDG_CACHE_HOME", Root: ".cache"},
				{Name: "XDG_CONFIG_HOME", Root: ".config"},
			},
			StateCitation: docOpenCodeStorage,
		},
		Skills: &Skills{
			Base:     "config",
			Env:      "",
			Parts:    []string{"opencode", valueSkills},
			Order:    4,
			Citation: "https://opencode.ai/docs/skills/",
		},
		MCP: &MCP{
			Scope:     valueCwd,
			Path:      "opencode.json",
			Format:    valueJSON,
			PrintOnly: true,
			Citation:  "https://opencode.ai/docs/mcp-servers/",
		},
	},
	// --agent 'pa' is the CLI name for Posit Assistant.
	// https://assistant.posit.co/docs/reference/tui/ — read 2026-11-17
	{
		Name: "pa",
		Launch: &Launch{
			Citation: "https://assistant.posit.co/docs/reference/tui/",
			Binary:   "pa", ModelFlag: modelFlag,
			Install: "curl -fsSL https://assistant.posit.co/builds/install-tui.sh | bash",
		},
	},
	// https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/quickstart.md — read 2026-11-17
	{
		Name: "pi",
		Launch: &Launch{
			Citation: docPiQuickstart,
			Binary:   "pi", ModelFlag: modelFlag,
			Args:           []string{"-p"},
			ResumeArgs:     []string{"-c", "--fork"},
			ResumeCitation: docPiQuickstart,
			// A denylist, so read/edit/write stay and only the two shell tools
			// go: `bash` on Unix, `powershell` on Windows.
			DiscoveryArgs:     []string{"--exclude-tools", "bash,powershell"},
			DiscoveryCitation: docPiCLI,
			// --no-approve ignores the reviewed tree's project-local files
			// (extensions, settings, MCP servers) without asking, and so
			// without the trust lock it would otherwise take in its agent
			// directory.
			// --no-session saves no session: verdict resumes none, and a later
			// `pi -c` in a directory would load one as its context.
			ReviewArgs:         []string{"--no-approve", "--no-session"},
			ReviewArgsCitation: docPiCLI,
			ReviewOwnedFlags: []string{
				"--approve", "-a", "--no-approve", "-na", "--mcp-project-servers", "--no-session", "--session-dir",
			},
			DiscoveryOwnedFlags: []string{"--exclude-tools", "-xt"},
			DiscoveryOverrideFlags: []string{
				"--tools", "-t", "--no-builtin-tools", "-nbt", "--no-tools", "-nt",
			},
			Install: "curl -fsSL https://pi.dev/install.sh | sh",
			// The agent directory (~/.pi/agent) holds auth.json and sessions
			// beside settings.json, models.json, extensions, skills, prompts,
			// themes, AGENTS.md and the managed install (bin, install, npm)
			// — per its docs, verified empirically against the agent's CLI.
			// auth.json is executable config (a `!command` value runs), so no
			// review writes it: every review launch gets a throwaway agent
			// directory in its wrapper seeded with its login, its models, and
			// the model and network keys of its settings — never extensions,
			// packages, skills, prompts, themes, or a command or path setting,
			// third-party code a review does not need.
			StateReadOnly: []string{".pi"},
			StateEphemeral: []EphemeralVar{{
				Name: "PI_CODING_AGENT_DIR", Root: ".pi/agent", Seed: []string{"auth.json", "models.json", "settings.json"},
				SeedKeys: map[string][]string{"settings.json": {
					"defaultProvider", "defaultModel", "defaultThinkingLevel", "modelThinkingLevels", "enabledModels",
					"thinkingBudgets", "transport", "retry", "httpProxy", "httpIdleTimeoutMs",
					"websocketConnectTimeoutMs", "websockets", "cacheWarming", "compaction",
				}},
			}},
			StateEnv:      []StateVar{{Name: "PI_CODING_AGENT_DIR", Root: ".pi/agent"}},
			StateCitation: docPiConfig,
		},
	},
	// --agent 'qoder' is the product slug.
	// https://docs.qoder.com/cli/run-in-scripts.md — read 2026-11-17
	{
		Name: "qoder",
		Launch: &Launch{
			Citation: "https://docs.qoder.com/cli/run-in-scripts.md",
			Binary:   "qoder", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "curl -fsSL https://qoder.com/install | bash",
		},
	},
	// --agent 'qodercn' is the CLI name for Qoder CN.
	// https://docs.qoder.cn/cli/run-in-scripts.md — read 2026-11-17
	{
		Name: "qodercn",
		Launch: &Launch{
			Citation: "https://docs.qoder.cn/cli/run-in-scripts.md",
			Binary:   "qodercn", ModelFlag: modelFlag,
			Args:    []string{"-p"},
			Install: "curl -fsSL https://static.qoder.com.cn/qoder-cli-cn/install.sh | bash",
		},
	},
	// https://github.com/QwenLM/qwen-code/blob/main/packages/cli/src/config/top-level-options.ts — read 2026-11-17
	{
		Name: "qwen",
		Launch: &Launch{
			Citation: "https://github.com/QwenLM/qwen-code/blob/main/packages/cli/src/config/top-level-options.ts",
			Binary:   "qwen", ModelFlag: modelFlag,
			Install: "npm install -g @qwen-code/qwen-code@latest",
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".qwen", valueSkills},
			Order:    8,
			Citation: "https://qwenlm.github.io/qwen-code-docs/en/users/features/skills/",
		},
		MCP: &MCP{
			Scope:     valueHome,
			Path:      ".qwen/settings.json",
			Format:    valueJSON,
			PrintOnly: false,
			Citation:  "https://qwenlm.github.io/qwen-code-docs/en/users/features/mcp/",
		},
	},
	// --agent 'rovodev' is the product slug; the binary is acli.
	// https://support.atlassian.com/rovo/docs/rovo-dev-cli-commands/ — read 2026-11-17
	{
		Name: "rovodev",
		Launch: &Launch{
			Citation: "https://support.atlassian.com/rovo/docs/rovo-dev-cli-commands/",
			Binary:   "acli",
			Args:     []string{"rovodev", argsRun},
			Install:  "install Atlassian CLI (acli)",
		},
	},
	// --agent 'vibe' is the CLI name for Mistral Vibe.
	// https://github.com/mistralai/mistral-vibe/blob/main/docs/programmatic-mode.md — read 2026-11-17
	{
		Name: "vibe",
		Launch: &Launch{
			Citation: "https://github.com/mistralai/mistral-vibe/blob/main/docs/programmatic-mode.md",
			Binary:   "vibe", ModelFlag: modelFlag, PromptFlag: "--prompt",
			Install: "curl -LsSf https://mistral.ai/vibe/install.sh | bash",
		},
	},
	// --agent 'zcode' is the product slug.
	// https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/usage/02-cli-reference.md — read 2026-11-17
	{
		Name: "zcode",
		Launch: &Launch{
			Citation: "https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/usage/02-cli-reference.md",
			Binary:   "zcode", PromptFlag: "-p",
			Install: "build @zcode/cli from https://github.com/zai-org/ZCode",
		},
	},
	{
		Name: "agents",
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".agents", valueSkills},
			Order:    0,
			Citation: "https://agentskills.io/client-implementation",
		},
	},
	{
		Name: "windsurf",
		Skills: &Skills{
			Base:     valueHome,
			Env:      "",
			Parts:    []string{".codeium", "windsurf", valueSkills},
			Order:    9,
			Citation: "https://docs.windsurf.com/windsurf/cascade/skills",
		},
		MCP: &MCP{
			Scope:     valueHome,
			Path:      ".codeium/windsurf/mcp_config.json",
			Format:    valueJSON,
			PrintOnly: false,
			Citation:  "https://docs.windsurf.com/windsurf/cascade/mcp",
		},
	},
}

// dirCodex is codex's home directory below HOME, envCodexHome the variable
// that relocates it, and fileCodexConfig its configuration file there.
const (
	dirCodex        = ".codex"
	envCodexHome    = "CODEX_HOME"
	fileCodexConfig = "config.toml"
)

// cloneEphemeral copies the ephemeral variables and their seeds.
func cloneEphemeral(in []EphemeralVar) []EphemeralVar {
	out := slices.Clone(in)
	for i := range out {
		out[i].Seed = slices.Clone(out[i].Seed)
		out[i].SeedKeys = maps.Clone(out[i].SeedKeys)
	}
	return out
}

// cloneProject copies the project state and its read-only list.
func cloneProject(in *ProjectState) *ProjectState {
	if in == nil {
		return nil
	}
	p := *in
	p.ReadOnly = slices.Clone(p.ReadOnly)
	return &p
}

// All returns independent data to prevent mutation of the catalog.
func All() []Agent {
	out := slices.Clone(catalog)
	for i := range out {
		a := &out[i]
		a.Aliases = slices.Clone(a.Aliases)
		if a.Launch != nil {
			v := *a.Launch
			v.Name = a.Name
			v.Args = slices.Clone(v.Args)
			v.ProbeArgs = slices.Clone(v.ProbeArgs)
			v.DiscoveryArgs = slices.Clone(v.DiscoveryArgs)
			v.DiscoveryOverrideFlags = slices.Clone(v.DiscoveryOverrideFlags)
			v.DiscoveryOwnedFlags = slices.Clone(v.DiscoveryOwnedFlags)
			v.ModelFlagAliases = slices.Clone(v.ModelFlagAliases)
			v.EditArgs = slices.Clone(v.EditArgs)
			v.PermissionFlags = slices.Clone(v.PermissionFlags)
			v.ReviewWriteArgs = slices.Clone(v.ReviewWriteArgs)
			v.SnapshotWriteArgs = slices.Clone(v.SnapshotWriteArgs)
			v.StateWritable = slices.Clone(v.StateWritable)
			v.StateReadOnly = slices.Clone(v.StateReadOnly)
			v.StateDirs = slices.Clone(v.StateDirs)
			v.StateEphemeral = cloneEphemeral(v.StateEphemeral)
			v.ProjectState = cloneProject(v.ProjectState)
			v.TempEnv = slices.Clone(v.TempEnv)
			v.StateEnv = slices.Clone(v.StateEnv)
			v.ReviewArgs = slices.Clone(v.ReviewArgs)
			v.ReviewOwnedFlags = slices.Clone(v.ReviewOwnedFlags)
			a.Launch = &v
		}
		if a.Skills != nil {
			v := *a.Skills
			v.Parts = slices.Clone(v.Parts)
			a.Skills = &v
		}
		if a.MCP != nil {
			v := *a.MCP
			a.MCP = &v
		}
	}
	return out
}

// Lookup resolves a canonical name; verbs select their own capabilities.
func Lookup(name string) (Agent, bool) {
	for _, a := range All() {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// StatePathShape checks one home-relative state entry, its trailing `*`
// removed: a clean, relative, slash-form path strictly below HOME. It says
// what is wrong, "" for a sound entry. The catalog's rows and an operator's
// prefs entries share it.
func StatePathShape(rel string) string {
	switch {
	case rel == "" || rel == ".":
		return "names HOME itself"
	case strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) || (len(rel) > 1 && rel[1] == ':'):
		return "must be relative to HOME, in slash form"
	case path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../"):
		return "must be a clean path below HOME"
	}
	return ""
}
