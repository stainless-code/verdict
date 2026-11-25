package agentusage

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// maxCodexConfigBytes bounds the config.toml read: a settings file, small.
const maxCodexConfigBytes = 1 << 20

// codexOpenAIProvider is the one model provider the price table prices.
const codexOpenAIProvider = "openai"

// CodexModel is the model a `codex exec` launch runs, and whether its usage
// can be priced from the OpenAI table. An explicit -m/--model or a
// `-c model=…` override wins; otherwise the selected profile's model
// (`-p/--profile`, else config.toml's `profile`), else config.toml's
// top-level `model`, read from home (Codex's CODEX_HOME). A local or
// open-source run (--oss, --local-provider) or a model_provider other than
// openai is never priced from the OpenAI table.
func CodexModel(home string, args []string) (string, bool) {
	flags := scanCodexArgs(args)
	if flags.local {
		return "", false
	}
	cfg := readCodexConfig(home)
	profile := flags.profile
	if profile == "" {
		profile = cfg.top["profile"]
	}
	provider := firstNonEmpty(flags.provider, cfg.profiles[profile]["model_provider"], cfg.top["model_provider"])
	if provider != "" && provider != codexOpenAIProvider {
		return "", false
	}
	model := firstNonEmpty(flags.model, cfg.profiles[profile]["model"], cfg.top["model"])
	return model, model != ""
}

// codexArgs is what a launch's own arguments say about model selection.
type codexArgs struct {
	model, profile, provider string
	local                    bool
}

// scanCodexArgs reads the model-selection options in the spaced and inline
// spellings.
func scanCodexArgs(args []string) codexArgs {
	var out codexArgs
	for i, arg := range args {
		name, value, inline := strings.Cut(arg, "=")
		if !inline && i+1 < len(args) {
			value = args[i+1]
		}
		switch name {
		case "-m", "--model":
			out.model = value
		case "-p", "--profile":
			out.profile = value
		case "-c", "--config":
			applyOverride(&out, value)
		case "--oss", "--local-provider":
			out.local = true
		}
	}
	return out
}

// applyOverride reads one `-c key=value` override of the model or provider.
func applyOverride(out *codexArgs, override string) {
	key, value, ok := strings.Cut(override, "=")
	if !ok {
		return
	}
	switch strings.TrimSpace(key) {
	case "model":
		out.model = unquote(value)
	case "model_provider":
		out.provider = unquote(value)
	}
}

// codexConfig is the string keys of config.toml: the top level and each
// [profiles.<name>] table.
type codexConfig struct {
	top      map[string]string
	profiles map[string]map[string]string
}

// readCodexConfig reads the string keys verdict needs from config.toml. An
// absent or unreadable file is an empty config; only `key = "string"` lines
// count, so anything richer is ignored rather than misread.
func readCodexConfig(home string) codexConfig {
	cfg := codexConfig{top: map[string]string{}, profiles: map[string]map[string]string{}}
	if home == "" {
		return cfg
	}
	raw, err := jq.CappedFile(filepath.Join(home, "config.toml"), maxCodexConfigBytes)
	if err == nil {
		cfg.parse(raw)
	}
	return cfg
}

// parse reads the string keys of each table in order.
func (c codexConfig) parse(raw []byte) {
	table := c.top
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if strings.HasPrefix(line, "[") {
			table = c.table(line)
			continue
		}
		if key, value, ok := stringKey(line); ok && table != nil {
			table[key] = value
		}
	}
}

// table is the map a [header] line selects: a profile's, or nil for any
// table verdict does not read.
func (c codexConfig) table(header string) map[string]string {
	name, ok := strings.CutPrefix(strings.Trim(header, "[] "), "profiles.")
	if !ok || name == "" {
		return nil
	}
	name = unquote(name)
	if c.profiles[name] == nil {
		c.profiles[name] = map[string]string{}
	}
	return c.profiles[name]
}

// stringKey reads one `key = "value"` line.
func stringKey(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
		return "", "", false
	}
	return strings.TrimSpace(key), value[1 : len(value)-1], true
}

// unquote strips one pair of matching quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
