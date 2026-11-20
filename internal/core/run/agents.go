package run

import (
	"slices"
	"strings"
)

// agentDef is one launch definition: the --agent name an operator types,
// the binary it spawns, the flag that binary takes a model on, the flag
// that takes its prompt as a value (empty for a positional prompt), and the
// non-interactive argument shape that binary uses to answer once and exit.
//
// The launcher reads every field and branches on none, so adding an agent is
// adding a row to [agentDefs] — data, not a code path. The one convention every
// row obeys is the argument order the launcher builds (see [argvFor]):
//
//	<binary> <args...> [<modelflag> <model>] [<passthrough...>] <prompt>
//	<binary> <args...> [<modelflag> <model>] [<passthrough...>] <promptflag> <prompt>
//
// The agent's own non-interactive flags come first so the operator's
// pass-through arguments cannot cancel them, the operator's flags come next so
// per-agent model/cost selection stays the operator's business, and the
// tool-generated directive is the last argument. A PromptFlag sits immediately
// before that directive, so a pass-through flag can never be swallowed as its
// value. A binary that wants its prompt somewhere else (a file path, stdin)
// does not fit this shape yet. ModelFlag is read only by a --print --stage
// render, which prints the stage's declared hint as that argument; a launch
// never composes one.
type agentDef struct {
	Name       string
	Binary     string
	Install    string
	ModelFlag  string
	PromptFlag string
	Source     string
	// ProbeMatch is the substring the launcher expects from the identity probe.
	ProbeMatch string
	// LaunchWarning is emitted once on stderr at launch when a headless CLI
	// carries a known reliability caveat.
	LaunchWarning string
	Args          []string
	// ProbeArgs runs the binary's harmless one-shot help before a launch; a
	// row declares it only when another tool shares the binary name.
	ProbeArgs []string
}

// modelFlag is the argument every shipped row takes a model name on; a row
// whose binary spells it differently carries its own value.
const modelFlag = "--model"

// agentSourcePrefs marks a definition read from the operator's prefs file in
// the provenance record. A shipped row leaves Source empty.
const agentSourcePrefs = "prefs"

// The four names whose prefix stays first in discovery messages, and the
// subcommand shared by the rows that launch one prompt through a run verb.
const (
	nameClaude   = "claude"
	nameCodex    = "codex"
	nameCursor   = "cursor"
	nameOpenCode = "opencode"
	argsRun      = "run"
	argsExec     = "exec"
	argsAsk      = "ask"
	argsHelp     = "--help"
)

// agyLaunchWarning flags Antigravity CLI's headless write soft-denial before
// launch: outside trustedWorkspaces, `agy -p` auto-denies file edits and exits
// 0 with no artifact, while approval flags stay with the operator after `--`.
const agyLaunchWarning = "agy print mode (-p) soft-denies file edits outside trusted workspaces, " +
	"so review.json may never be written; trust this repository in agy's settings or pass " +
	"-- --dangerously-skip-permissions (your approval bypass)"

// agentDefs is the shipped agent table, sorted by name. Every row is data: a
// new agent is a new entry here, and nothing else in the launcher changes.
// Permission and approval flags stay with the operator via `-- <args>`.
var agentDefs = []agentDef{
	// --agent 'adal' is Sylph's product slug.
	// https://docs.sylph.ai/integration/headless.md — read 2026-11-17
	{
		Name: "adal", Binary: "adal", ModelFlag: modelFlag, PromptFlag: "-q",
		Install: "curl -fsSL https://adal.sylph.ai/install.sh | bash",
	},
	// https://github.com/google-antigravity/antigravity-cli — read 2026-11-17
	{
		Name: "agy", Binary: "agy", ModelFlag: modelFlag, PromptFlag: "-p",
		LaunchWarning: agyLaunchWarning,
		Install:       "curl -fsSL https://antigravity.google/cli/install.sh | bash",
	},
	// https://ampcode.com/docs/markdown/cli/execute-mode — read 2026-11-17
	{
		Name: "amp", Binary: "amp", ModelFlag: modelFlag, PromptFlag: "-x",
		Install: "npm install -g @ampcode/cli",
	},
	// --agent 'auggie' is the CLI name for Augment.
	// https://docs.augmentcode.com/cli/reference.md — read 2026-11-17
	{
		Name: "auggie", Binary: "auggie", ModelFlag: modelFlag,
		Args:    []string{"--print"},
		Install: "npm install -g @augmentcode/auggie@prerelease",
	},
	// --agent 'autohand' is the CLI name for Autohand Code.
	// https://github.com/autohandai/code-cli/blob/main/src/index.ts — read 2026-11-17
	{
		Name: "autohand", Binary: "autohand", ModelFlag: modelFlag,
		Install: "curl -fsSL https://autohand.ai/install.sh | bash",
	},
	// brew info bob: neovim version manager; brew search --formula /^bob$/ -> bob (clash).
	// `--disable-tool-groups <groups>` "Disable specific tool groups (comma-separated, for example execute,mcp)"
	// https://bob.ibm.com/docs/shell/getting-started/start-bobshell-non-interactive — read 2026-11-17
	{
		Name: "bob", Binary: "bob",
		Args:      []string{argsRun},
		ProbeArgs: []string{argsRun, argsHelp}, ProbeMatch: "--disable-tool-groups",
		Install: "curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash",
	},
	// https://code.claude.com/docs/en/cli-reference.md — read 2026-11-17
	{
		Name: nameClaude, Binary: nameClaude, ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "curl -fsSL https://claude.ai/install.sh | bash",
	},
	// One-shot: `cline "your prompt"` runs a single turn in yolo mode and exits.
	// https://github.com/cline/cline/blob/main/apps/cli/README.md — read 2026-11-17
	{
		Name: "cline", Binary: "cline", ModelFlag: modelFlag,
		Install: "npm install -g cline",
	},
	// --agent 'codearts' is the CLI name for CodeArts Agent.
	// https://support.huaweicloud.com/usermanual-cli/codeartsagent_cli_0034.html — read 2026-11-17
	{
		Name: "codearts", Binary: "codearts", ModelFlag: modelFlag,
		Args: []string{argsRun},
		Install: "sh -c \"$(curl -L https://cnnorth4-cloudide-marketplace.obs.cn-north-4.myhuaweicloud.com/" +
			"codearts/cli_tui/install_script/install.sh)\"",
	},
	// https://www.codebuddy.ai/docs/cli/headless — read 2026-11-17
	{
		Name: "codebuddy", Binary: "codebuddy", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "npm install -g @tencent-ai/codebuddy-code",
	},
	// https://github.com/openai/codex/blob/main/README.md — read 2026-11-17
	{
		Name: nameCodex, Binary: nameCodex, ModelFlag: modelFlag,
		Args:    []string{argsExec},
		Install: "npm install -g @openai/codex",
	},
	// https://commandcode.ai/docs/headless — read 2026-11-17
	{
		Name: "commandcode", Binary: "commandcode", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "npm i -g command-code@latest",
	},
	// --agent 'continue' is Continue's product slug; the binary is cn.
	// https://github.com/continuedev/continue/blob/main/docs/cli/quickstart.mdx — read 2026-11-17
	{
		Name: "continue", Binary: "cn", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "npm i -g @continuedev/cli",
	},
	// https://docs.github.com/en/copilot/how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically — read 2026-11-17
	{
		Name: "copilot", Binary: "copilot", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "npm install -g @github/copilot",
	},
	// brew info cortex: Prometheus long-term storage; brew search --formula /^cortex$/ -> cortex (clash).
	// `--bypass, --dangerously-allow-all-tool-calls` "Enable bypass mode, automatically approving all tool calls"
	// https://docs.snowflake.com/en/user-guide/cortex-code/cli-reference — read 2026-11-17
	{
		Name: "cortex", Binary: "cortex", ModelFlag: modelFlag, PromptFlag: "-p",
		ProbeArgs: []string{argsHelp}, ProbeMatch: "--dangerously-allow-all-tool-calls",
		Install: "curl -qLsS https://ai.snowflake.com/static/cc-scripts/install.sh | sh",
	},
	// https://github.com/charmbracelet/crush/blob/main/internal/cmd/run.go — read 2026-11-17
	{
		Name: "crush", Binary: "crush", ModelFlag: modelFlag,
		Args:    []string{argsRun},
		Install: "brew install charmbracelet/tap/crush",
	},
	// https://cursor.com/docs/cli/headless — read 2026-11-17
	{
		Name: "cursor", Binary: "cursor-agent", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "curl https://cursor.com/install -fsS | bash",
	},
	// --agent 'dcode' is the CLI name for Deep Agents.
	// https://github.com/langchain-ai/deepagents/blob/main/libs/code/deepagents_code/main.py — read 2026-11-17
	{
		Name: "dcode", Binary: "dcode", ModelFlag: modelFlag, PromptFlag: "-n",
		Install: "curl -LsSf https://langch.in/dcode | bash",
	},
	// --agent 'devin' is the product slug for Devin for Terminal.
	// https://docs.devin.ai/cli/reference/commands.md — read 2026-11-17
	{
		Name: "devin", Binary: "devin", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "curl -fsSL https://cli.devin.ai/install.sh | bash",
	},
	// https://github.com/truffle-ai/dexto/blob/main/packages/cli/src/cli/commands/run/register.ts — read 2026-11-17
	{
		Name: "dexto", Binary: "dexto", ModelFlag: modelFlag,
		Args:    []string{argsRun},
		Install: "npm install -g dexto",
	},
	// https://docs.factory.com/droid-cli/cli-reference.md — read 2026-11-17
	{
		Name: "droid", Binary: "droid", ModelFlag: modelFlag,
		Args:    []string{argsExec},
		Install: "curl -fsSL https://app.factory.ai/cli | sh",
	},
	// brew info forge: ArrayFire visualization; brew search --formula /^forge$/ -> forge (clash).
	// `#[arg(long, alias = "cid")] pub conversation_id: Option<ConversationId>,`
	// https://github.com/tailcallhq/forgecode/blob/main/crates/forge_main/src/cli.rs — read 2026-11-17
	{
		Name: "forge", Binary: "forge", PromptFlag: "-p",
		ProbeArgs: []string{argsHelp}, ProbeMatch: "--conversation-id",
		Install: "curl -fsSL https://forgecode.dev/cli | sh",
	},
	// brew info fx: terminal JSON viewer; brew search --formula /^fx$/ -> fx (clash).
	// `fx ask` documents `--model`, `--effort`, `--fast`, `--ultrafast` per request
	// https://fx.sh/docs/using-fx/fx-ask — read 2026-11-17
	{
		Name: "fx", Binary: "fx", ModelFlag: modelFlag,
		Args:      []string{argsAsk},
		ProbeArgs: []string{argsAsk, argsHelp}, ProbeMatch: "--ultrafast",
		Install: "curl -fsSL https://fx.sh/setup.sh | bash",
	},
	// https://github.com/google-gemini/gemini-cli/blob/main/README.md — read 2026-11-17
	{
		Name: "gemini", Binary: "gemini", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "npm install -g @google/gemini-cli",
	},
	// https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/running-tasks.md — read 2026-11-17
	{
		Name: "goose", Binary: "goose", ModelFlag: modelFlag, PromptFlag: "-t",
		Args:    []string{argsRun},
		Install: "brew install block-goose-cli",
	},
	// brew info grok: regex tool; brew search --formula /^grok$/ -> grok (clash).
	// headless flags list `--prompt-json <JSON>`
	// https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/14-headless-mode.md — read 2026-11-17
	{
		Name: "grok", Binary: "grok", ModelFlag: modelFlag, PromptFlag: "-p",
		ProbeArgs: []string{argsHelp}, ProbeMatch: "--prompt-json",
		Install: "curl -fsSL https://x.ai/cli/install.sh | bash",
	},
	// --agent 'hermes' is the CLI name for Hermes Agent.
	// https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/_parser.py — read 2026-11-17
	{
		Name: "hermes", Binary: "hermes", ModelFlag: modelFlag, PromptFlag: "-z",
		Install: "curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash",
	},
	// brew info jazz: no formula; brew search --formula /^jazz$/ -> none (no known clash, no probe).
	// jazz run needs --agent <id>; pass it after '--'.
	// https://github.com/lvndry/jazz/blob/main/docs/commands.md — read 2026-11-17
	{
		Name: "jazz", Binary: "jazz",
		Args:    []string{argsRun},
		Install: "curl -fsSL https://github.com/lvndry/jazz/releases/latest/download/install.sh | bash",
	},
	// --model is confirmed in the Junie CLI reference (parameters.html, read 2026-11-17).
	// https://junie.jetbrains.com/docs/junie-headless.html — read 2026-11-17
	{
		Name: "junie", Binary: "junie", ModelFlag: modelFlag,
		Install: "curl -fsSL https://junie.jetbrains.com/install.sh | bash",
	},
	// brew info kilo: no formula; brew search --formula /^kilo$/ -> none; antirez/kilo is a text editor (clash).
	// `.option("cloud-fork", { … describe: "fetch session from cloud and continue locally (use with --session)"`
	// https://github.com/Kilo-Org/kilocode/blob/main/packages/opencode/src/cli/cmd/run.ts — read 2026-11-17
	{
		Name: "kilo", Binary: "kilo", ModelFlag: modelFlag,
		Args:      []string{argsRun},
		ProbeArgs: []string{argsRun, argsHelp}, ProbeMatch: "--cloud-fork",
		Install: "npm install -g @kilocode/cli",
	},
	// --agent 'kimchi' is the product slug.
	// https://docs.kimchi.dev/docs/coding-headless.md — read 2026-11-17
	{
		Name: "kimchi", Binary: "kimchi", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "curl -fsSL https://github.com/getkimchi/kimchi/releases/latest/download/install.sh | bash",
	},
	// --agent 'kimi' is the CLI name for Kimi Code.
	// https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/reference/kimi-command.md — read 2026-11-17
	{
		Name: "kimi", Binary: "kimi", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash",
	},
	// https://kiro.dev/docs/cli/headless.md — read 2026-11-17
	{
		Name: "kiro", Binary: "kiro-cli", ModelFlag: modelFlag,
		Args:    []string{"chat", "--no-interactive"},
		Install: "curl -fsSL https://cli.kiro.dev/install | bash",
	},
	// --agent 'kode' is the product slug.
	// https://github.com/shareAI-lab/Kode-CLI/blob/main/apps/cli/src/entrypoints/cli/cliParser/program.tsx — read 2026-11-17
	{
		Name: "kode", Binary: "kode", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "npm install -g @shareai-lab/kode",
	},
	// --agent 'mcode' is the CLI name for MiniMax Code.
	// https://github.com/MiniMax-AI/minimax-code/blob/main/packages/tui/src/cli/contract.ts — read 2026-11-17
	{
		Name: "mcode", Binary: "mcode", ModelFlag: modelFlag,
		Args:    []string{argsExec},
		Install: "curl -fsSL https://filecdn.minimax.chat/public/install.sh | bash",
	},
	// https://github.com/neovateai/neovate-code/blob/master/src/index.ts — read 2026-11-17
	{
		Name: "neovate", Binary: "neovate", ModelFlag: modelFlag,
		Args:    []string{"-q"},
		Install: "npm install -g @neovate/code",
	},
	// --agent 'openclaw' is the product slug.
	// https://github.com/openclaw/openclaw/blob/main/docs/cli/agent.md — read 2026-11-17
	{
		Name: "openclaw", Binary: "openclaw", ModelFlag: modelFlag,
		Args:    []string{"agent", argsExec},
		Install: "curl -fsSL https://openclaw.ai/install.sh | bash",
	},
	// https://opencode.ai/docs/cli/ — read 2026-11-17
	{
		Name: nameOpenCode, Binary: nameOpenCode, ModelFlag: modelFlag,
		Args:    []string{argsRun},
		Install: "npm install -g @opencode/cli",
	},
	// --agent 'pa' is the CLI name for Posit Assistant.
	// https://assistant.posit.co/docs/reference/tui/ — read 2026-11-17
	{
		Name: "pa", Binary: "pa", ModelFlag: modelFlag,
		Install: "curl -fsSL https://assistant.posit.co/builds/install-tui.sh | bash",
	},
	// https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/quickstart.md — read 2026-11-17
	{
		Name: "pi", Binary: "pi", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "curl -fsSL https://pi.dev/install.sh | sh",
	},
	// --agent 'qoder' is the product slug.
	// https://docs.qoder.com/cli/run-in-scripts.md — read 2026-11-17
	{
		Name: "qoder", Binary: "qoder", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "curl -fsSL https://qoder.com/install | bash",
	},
	// --agent 'qodercn' is the CLI name for Qoder CN.
	// https://docs.qoder.cn/cli/run-in-scripts.md — read 2026-11-17
	{
		Name: "qodercn", Binary: "qodercn", ModelFlag: modelFlag,
		Args:    []string{"-p"},
		Install: "curl -fsSL https://static.qoder.com.cn/qoder-cli-cn/install.sh | bash",
	},
	// https://github.com/QwenLM/qwen-code/blob/main/packages/cli/src/config/top-level-options.ts — read 2026-11-17
	{
		Name: "qwen", Binary: "qwen", ModelFlag: modelFlag,
		Install: "npm install -g @qwen-code/qwen-code@latest",
	},
	// --agent 'rovodev' is the product slug; the binary is acli.
	// https://support.atlassian.com/rovo/docs/rovo-dev-cli-commands/ — read 2026-11-17
	{
		Name: "rovodev", Binary: "acli",
		Args:    []string{"rovodev", argsRun},
		Install: "install Atlassian CLI (acli)",
	},
	// --agent 'vibe' is the CLI name for Mistral Vibe.
	// https://github.com/mistralai/mistral-vibe/blob/main/docs/programmatic-mode.md — read 2026-11-17
	{
		Name: "vibe", Binary: "vibe", ModelFlag: modelFlag, PromptFlag: "--prompt",
		Install: "curl -LsSf https://mistral.ai/vibe/install.sh | bash",
	},
	// --agent 'zcode' is the product slug.
	// https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/usage/02-cli-reference.md — read 2026-11-17
	{
		Name: "zcode", Binary: "zcode", PromptFlag: "-p",
		Install: "build @zcode/cli from https://github.com/zai-org/ZCode",
	},
}

// lookupAgent resolves one --agent name against the table.
func lookupAgent(name string) (agentDef, bool) {
	for i := range agentDefs {
		def := &agentDefs[i]
		if def.Name == name {
			return *def, true
		}
	}
	return agentDef{}, false
}

// AgentNames returns the known agent names, for the unknown-agent refusal and
// the other verbs that list them. The four rows shipped before STA-440 keep
// their prefix, so a caller that matched that fragment still finds it; the rest
// follow in table order.
func AgentNames() []string {
	first := []string{nameClaude, nameCodex, nameCursor, nameOpenCode}
	names := make([]string, 0, len(agentDefs))
	for _, name := range first {
		if _, ok := lookupAgent(name); ok {
			names = append(names, name)
		}
	}
	for i := range agentDefs {
		name := agentDefs[i].Name
		if !slices.Contains(first, name) {
			names = append(names, name)
		}
	}
	return names
}

// argvFor builds the exact argv the launcher spawns, and the same argv
// --print renders: the binary, the definition's non-interactive flags, the
// model hint a --print --stage render declares (empty for everything else),
// the operator's pass-through arguments unchanged, an optional prompt flag,
// then the directive.
func argvFor(def *agentDef, model string, passthrough []string, prompt string) []string {
	var argv []string
	argv = append(argv, def.Binary)
	argv = append(argv, def.Args...)
	if model != "" && def.ModelFlag != "" {
		argv = append(argv, def.ModelFlag, model)
	}
	argv = append(argv, passthrough...)
	if def.PromptFlag != "" {
		argv = append(argv, def.PromptFlag)
	}
	return append(argv, prompt)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
