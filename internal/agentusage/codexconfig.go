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
// can be priced from the OpenAI table. Codex's own precedence decides it:
// -m/--model over the merged configuration, where a `-c model=…` override,
// the selected profile's model, and config.toml's top-level model layer.
// The profile is -p/--profile, a `-c profile=…` override, or config.toml's
// `profile`; its model is read from both a [profiles.<name>] table and the
// <name>.config.toml file Codex now keeps beside config.toml. Where the order
// between an override and a profile is not settled by Codex's source, the two
// must agree: a disagreement, or a relevant key verdict cannot read, is no
// price at all, never the cheaper guess. A local or open-source run (--oss,
// --local-provider) or any model_provider other than openai is never priced
// from the OpenAI table.
func CodexModel(home string, args []string) (string, bool) {
	flags := scanCodexArgs(args)
	if flags.local || flags.unclear {
		return "", false
	}
	cfg := readCodexFile(filepath.Join(home, "config.toml"), home != "")
	sel, ok := cfg.selection(home, &flags)
	if !ok || !openAI(flags.provider, sel.provider, cfg.top["model_provider"]) {
		return "", false
	}
	if flags.model != "" {
		return flags.model, true
	}
	model, ok := agreed(flags.cfgModel, sel.model)
	if !ok {
		return "", false
	}
	model = firstNonEmpty(model, cfg.top["model"])
	return model, model != ""
}

// codexArgs is what a launch's own arguments say about model selection: the
// -m and -p flags apart from their `-c` overrides, which Codex layers
// differently.
type codexArgs struct {
	model, cfgModel, profile, cfgProfile, provider string
	local, unclear                                 bool
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

// applyOverride reads one `-c key=value` override. Codex parses the value as
// TOML and falls back to the raw string, so either spelling is read; an
// override of a model key verdict cannot place (a dotted profile path) makes
// the launch unpriced.
func applyOverride(out *codexArgs, override string) {
	key, value, ok := strings.Cut(override, "=")
	if !ok {
		return
	}
	switch key = strings.TrimSpace(key); key {
	case codexKeyModel:
		out.cfgModel = unquote(value)
	case codexKeyProvider:
		out.provider = unquote(value)
	case codexKeyProfile:
		out.cfgProfile = unquote(value)
	default:
		out.unclear = out.unclear || relevantKey(key)
	}
}

// The config keys that choose what a launch is priced as.
const (
	codexKeyModel    = "model"
	codexKeyProvider = "model_provider"
	codexKeyProfile  = "profile"
)

// relevantKey reports a key, dotted or not, that names one of the keys that
// choose the model.
func relevantKey(key string) bool {
	last := unquote(key[strings.LastIndex(key, ".")+1:])
	return last == codexKeyModel || last == codexKeyProvider || last == codexKeyProfile
}

// openAI reports whether every provider any layer names is OpenAI's.
func openAI(providers ...string) bool {
	for _, p := range providers {
		if p != "" && p != codexOpenAIProvider {
			return false
		}
	}
	return true
}

// agreed is the one value two sources name: either alone, or both when they
// match. false when they disagree.
func agreed(a, b string) (string, bool) {
	if a != "" && b != "" && a != b {
		return "", false
	}
	return firstNonEmpty(a, b), true
}

// codexConfig is the string keys of one Codex config file: the top level and
// each [profiles.<name>] table. unclear marks a model, provider, or profile
// key it could not read.
type codexConfig struct {
	top      map[string]string
	profiles map[string]map[string]string
	unclear  bool
}

// profileSelection is the selected profile's model and provider.
type profileSelection struct {
	model, provider string
}

// selection resolves the profile the launch selects and reads its model
// from both places Codex keeps a profile. false when anything that decides
// the price is unreadable or contradicts itself.
func (c *codexConfig) selection(home string, flags *codexArgs) (profileSelection, bool) {
	name, ok := agreed(flags.profile, flags.cfgProfile)
	if c.unclear || !ok {
		return profileSelection{}, false
	}
	name = firstNonEmpty(name, c.top[codexKeyProfile])
	if name == "" {
		return profileSelection{}, true
	}
	if !safeProfileName(name) {
		return profileSelection{}, false
	}
	table := c.profiles[name]
	file := readCodexFile(filepath.Join(home, name+".config.toml"), home != "")
	model, modelOK := agreed(table[codexKeyModel], file.top[codexKeyModel])
	provider, providerOK := agreed(table[codexKeyProvider], file.top[codexKeyProvider])
	return profileSelection{model: model, provider: provider}, !file.unclear && modelOK && providerOK
}

// safeProfileName reports a profile name that names a file in CODEX_HOME and
// nothing outside it.
func safeProfileName(name string) bool {
	return name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// readCodexFile reads the string keys verdict needs from one config file. An
// absent or unreadable file is an empty config.
func readCodexFile(path string, read bool) codexConfig {
	cfg := codexConfig{top: map[string]string{}, profiles: map[string]map[string]string{}}
	if !read {
		return cfg
	}
	raw, err := jq.CappedFile(path, maxCodexConfigBytes)
	if err == nil {
		cfg.parse(raw)
	}
	return cfg
}

// parse reads the string keys of each table in order, comments stripped.
func (c *codexConfig) parse(raw []byte) {
	table := c.top
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(nil, maxCodexConfigBytes)
	for scan.Scan() {
		line := strings.TrimSpace(stripComment(scan.Text()))
		switch {
		case line == "":
		case strings.HasPrefix(line, "["):
			table = c.table(line)
		case table != nil:
			c.setKey(table, line)
		}
	}
}

// setKey reads one `key = "value"` line of a table verdict reads. A model,
// provider, or profile key whose value is not one plain string — or that
// reaches into another table by a dotted path — makes the config unclear.
func (c *codexConfig) setKey(table map[string]string, line string) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return
	}
	key = strings.TrimSpace(key)
	if !relevantKey(key) {
		// A profile written as an inline table hides its model from the
		// line reader.
		c.unclear = c.unclear || inlineModel(value)
		return
	}
	str, ok := tomlString(value)
	if !ok || strings.Contains(key, ".") {
		c.unclear = true
		return
	}
	table[unquote(key)] = str
}

// table is the map a [header] line selects: a profile's, or nil for any
// table verdict does not read.
func (c *codexConfig) table(header string) map[string]string {
	if strings.Trim(header, "[] ") == "profiles" {
		return map[string]string{}
	}
	name, ok := strings.CutPrefix(strings.Trim(header, "[] "), "profiles.")
	if !ok || name == "" || strings.HasPrefix(header, "[[") {
		return nil
	}
	name = unquote(name)
	if c.profiles[name] == nil {
		c.profiles[name] = map[string]string{}
	}
	return c.profiles[name]
}

// inlineModel reports an inline table that sets a model key.
func inlineModel(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") && strings.Contains(value, codexKeyModel)
}

// stripComment cuts a trailing `# comment` that sits outside any quoted
// string.
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		if quote == 0 && line[i] == '#' {
			return line[:i]
		}
		quote, i = advanceQuote(line, i, quote)
	}
	return line
}

// advanceQuote moves the quoted-string state past line[i], skipping an
// escaped character inside a basic string.
func advanceQuote(line string, i int, quote byte) (byte, int) {
	switch ch := line[i]; {
	case quote == '"' && ch == '\\':
		return quote, i + 1
	case quote != 0 && ch == quote:
		return 0, i
	case quote == 0 && (ch == '"' || ch == '\''):
		return ch, i
	}
	return quote, i
}

// tomlString reads a value that is one plain quoted string, without escapes.
func tomlString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
		return "", false
	}
	inner := value[1 : len(value)-1]
	if strings.ContainsRune(inner, rune(value[0])) || strings.ContainsRune(inner, '\\') {
		return "", false
	}
	return inner, true
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
