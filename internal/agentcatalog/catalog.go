// Package agentcatalog owns the capabilities of shipped agents.
package agentcatalog

import "slices"

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
	Usage              *Usage
	KeyEnvCitation     string
	Citation           string
	Name               string
	Binary             string
	Install            string
	ModelFlag          string
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
	ReviewWriteCitation    string
	ProbeArgs              []string
	DiscoveryArgs          []string
	ResumeArgs             []string
	DiscoveryOverrideFlags []string
	Args                   []string
	// EditArgs is the documented flag set that lets the CLI write files in its
	// working directory non-interactively without granting the shell or
	// skipping permission checks. Only `verdict loop` applies it, to its fix
	// spawn — whose contract is editing the working tree — and an operator's
	// own `--` arguments replace it. A row with no equally scoped flag leaves
	// it and EditCitation empty: an approval bypass is never an edit grant.
	EditArgs []string
	// ReviewWriteArgs is the documented grant that lets a review launch write
	// its artifacts without a prompt, scoped to the launch's own output
	// directory: [ReviewDir] in each argument is replaced by that directory as
	// the CLI's rule path. verdict run adds it to every review launch unless
	// the operator's `--` arguments already decide tool permissions (a
	// DiscoveryOverrideFlags spelling). A row without an equally scoped
	// option leaves both it and ReviewWriteCitation empty.
	ReviewWriteArgs []string
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
	docOpenCodeRun  = "https://opencode.ai/docs/cli/commands"
	docCodexReadme  = "https://github.com/openai/codex/blob/main/README.md"
	docOpenCodeCLI  = "https://opencode.ai/docs/cli/"
	docPiCLI        = "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/cli.md"
	docPiQuickstart = "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/quickstart.md"
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
			DiscoveryArgs:          []string{flagDisableToolGroups, "execute,mcp"},
			DiscoveryCitation:      docBobShell,
			DiscoveryOverrideFlags: []string{flagDisableToolGroups},
			Install:                "curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash",
		},
	},
	// https://code.claude.com/docs/en/cli-reference.md — read 2026-11-17
	{
		Name: "claude",
		Launch: &Launch{
			Citation:           docClaudeCLI,
			PermissionHint:     "--permission-mode acceptEdits",
			PermissionCitation: docClaudeCLI,
			EditArgs:           []string{"--permission-mode", "acceptEdits"},
			EditCitation:       docClaudeCLI,
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
			DiscoveryOverrideFlags: []string{
				"--disallowedTools", "--disallowed-tools", "--allowedTools", "--allowed-tools", "--tools",
			},
			Usage: &Usage{
				Format: UsageClaudeJSON, Citation: docClaudeHeadless,
				Args: []string{"--output-format", "json"},
			},
			Install: "curl -fsSL https://claude.ai/install.sh | bash",
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
			EditArgs:           []string{"--sandbox", "workspace-write"},
			EditCitation:       docCodexReadme,
			KeyEnv:             "OPENAI_API_KEY",
			KeyEnvCitation:     docCodexReadme,
			Binary:             nameCodex, ModelFlag: modelFlag,
			Args:           []string{argsExec},
			ResumeArgs:     []string{"resume"},
			ResumeCitation: docCodexReadme,
			Usage:          &Usage{Format: UsageCodexJSONL, Citation: docCodexExecJSON, Args: []string{"--json"}},
			Install:        "npm install -g @openai/codex",
		},
		Skills: &Skills{
			Base:     valueHome,
			Env:      "CODEX_HOME",
			Parts:    []string{".codex", valueSkills},
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
			DiscoveryOverrideFlags: []string{
				"--exclude-tools", "-xt", "--tools", "-t", "--no-builtin-tools", "-nbt", "--no-tools", "-nt",
			},
			Install: "curl -fsSL https://pi.dev/install.sh | sh",
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
			v.EditArgs = slices.Clone(v.EditArgs)
			v.ReviewWriteArgs = slices.Clone(v.ReviewWriteArgs)
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
