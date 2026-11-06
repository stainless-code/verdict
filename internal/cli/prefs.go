package cli

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// SettingsPath returns the settings file path from HOME/XDG_CONFIG_HOME.
func SettingsPath(home, xdg string) string {
	base := home + "/.config"
	if xdg != "" {
		base = xdg
	}
	return filepath.Join(base, "verdict", "settings.json")
}

var credRe = regexp.MustCompile(`token|secret|passwd|password|pwd|auth|bearer|credential|pat|key`)

// PrefsResult carries the outcome of preference resolution.
type PrefsResult struct {
	Effective Effective
	Path      string
}

// ResolvePrefs resolves the preference layers in order: env layer, then the
// settings file layer. Warnings ride the run's warn lines; file problems
// return typed refusals. readFile injects
// file reads; env maps the process environment.
func ResolvePrefs(r *Runner, env map[string]string, readFile func(string) ([]byte, error)) (*PrefsResult, error) {
	eff := Defaults()
	warn := func(msg string) { r.Warn(msg) }
	r.Output = formatText

	resolveOutputEnv(r, env, warn)
	resolveCompactEnv(env, &eff, warn)
	resolveColorEnv(env, &eff, warn)

	path := SettingsPath(env["HOME"], env["XDG_CONFIG_HOME"])
	resolvePublishEnv(env, &eff, warn)

	raw, err := readFile(path)
	if err != nil {
		return gateMissingFile(&eff, path)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgNotJSON, path)}
	}
	obj, ok := jq.AsMap(doc)
	if !ok {
		return nil, &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgNotObject, path)}
	}
	return resolveSettingsFile(r, path, obj, doc, &eff)
}

func resolveOutputEnv(r *Runner, env map[string]string, warn func(string)) {
	switch v := env["REVIEW_OUTPUT_FORMAT"]; v {
	case "":
	case formatText, formatJSON:
		r.Output = v
		if v == formatJSON {
			r.JSONMode = true
		}
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_OUTPUT_FORMAT '%s' (want text|json)", v))
	}
}

func resolveCompactEnv(env map[string]string, eff *Effective, warn func(string)) {
	switch c := env["REVIEW_LAYOUT_COMPACT"]; c {
	case "":
	case FlagOn, FlagOff:
		eff.Compact = c
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_LAYOUT_COMPACT '%s' (want true|false)", c))
	}
}

func resolveColorEnv(env map[string]string, eff *Effective, warn func(string)) {
	switch c := env["REVIEW_COLOR_MODE"]; c {
	case "":
	case colorAuto, colorAlways, colorNever:
		eff.Color = c
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_COLOR_MODE '%s' (want auto|always|never)", c))
	}
}

func gateMissingFile(eff *Effective, path string) (*PrefsResult, error) {
	if ref := ValidateAttributionPlaceholders(eff.PublishAttributionText, "publish.attribution_text"); ref != nil {
		return nil, ref
	}
	return &PrefsResult{Effective: *eff, Path: path}, nil
}

// checkSettingsShape refuses a document that names a credential key or that
// carries a key outside the recognized top-level set or schema version. It is
// split out of resolveSettingsFile so each refusal stays one readable step.
func checkSettingsShape(path string, obj map[string]any, doc any) error {
	if n := countCredKeys(doc); n > 0 {
		return &RefusalError{Code: "bad-settings-credentials", Message: fmt.Sprintf(msgCredKey, path)}
	}
	if ref := checkTopKeys(path, obj); ref != nil {
		return ref
	}
	return checkSchemaKey(path, obj)
}

func resolveSettingsFile(r *Runner, path string, obj map[string]any, doc any, eff *Effective) (*PrefsResult, error) {
	if ref := checkSettingsShape(path, obj, doc); ref != nil {
		return nil, ref
	}
	if ref := resolveOutputFile(r, path, obj); ref != nil {
		return nil, ref
	}
	if ref := resolveLayoutFile(path, obj, eff); ref != nil {
		return nil, ref
	}
	if ref := resolveColorFile(r, path, obj, eff); ref != nil {
		return nil, ref
	}
	if ref := resolvePublishFile(path, obj, eff); ref != nil {
		return nil, ref
	}
	if ref := validatePersonalSections(obj); ref != nil {
		return nil, ref
	}
	return &PrefsResult{Effective: *eff, Path: path}, nil
}

func checkTopKeys(path string, obj map[string]any) error {
	for _, k := range SortedKeys(obj) {
		switch k {
		case "$schema", "output", "layout", "color", "publish", "review", "spend", "privacy":
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgTopKey, path, k)}
		}
	}
	return nil
}

func checkSchemaKey(path string, obj map[string]any) error {
	if _, ok := obj["$schema"]; ok {
		if _, isStr := obj["$schema"].(string); !isStr {
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgSchemaType, path)}
		}
	}
	return nil
}

func resolveOutputFile(r *Runner, path string, obj map[string]any) error {
	v, ok := obj["output"]
	if !ok {
		return nil
	}
	vo, isObj := jq.AsMap(v)
	if !isObj {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgOutputObj, path)}
	}
	for _, k := range SortedKeys(vo) {
		if k != "format" {
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgOutputKey, path, k)}
		}
	}
	f, ok := vo["format"]
	if !ok {
		return nil
	}
	return applyOutputFormat(r, path, f)
}

func applyOutputFormat(r *Runner, path string, f any) error {
	fs, isStr := f.(string)
	if !isStr {
		fs = jq.Str(f)
	}
	switch fs {
	case formatText, formatJSON:
		r.Output = fs
		r.JSONMode = fs == formatJSON || r.JSONPeeked
		return nil
	default:
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgOutputFormat, path, jq.Str(f))}
	}
}

func resolveLayoutFile(path string, obj map[string]any, eff *Effective) error {
	v, ok := obj["layout"]
	if !ok {
		return nil
	}
	vo, isObj := jq.AsMap(v)
	if !isObj {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgLayoutObj, path)}
	}
	for _, k := range SortedKeys(vo) {
		if k != "compact" {
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgLayoutKey, path, k)}
		}
	}
	if c, ok := vo["compact"]; ok {
		return applyCompact(path, c, eff)
	}
	return nil
}

func applyCompact(path string, c any, eff *Effective) error {
	b, isBool := c.(bool)
	if !isBool {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgLayoutBool, path)}
	}
	if b {
		eff.Compact = FlagOn
	} else {
		eff.Compact = FlagOff
	}
	return nil
}

func resolveColorFile(r *Runner, path string, obj map[string]any, eff *Effective) error {
	v, ok := obj["color"]
	if !ok {
		return nil
	}
	vo, isObj := jq.AsMap(v)
	if !isObj {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgColorObj, path)}
	}
	for _, k := range SortedKeys(vo) {
		if k != "mode" {
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgColorKey, path, k)}
		}
	}
	if m, ok := vo["mode"]; ok {
		return applyColorMode(r, path, m, eff)
	}
	return nil
}

func applyColorMode(r *Runner, path string, m any, eff *Effective) error {
	ms, isStr := m.(string)
	if !isStr {
		ms = jq.Str(m)
	}
	switch ms {
	case colorAuto, colorAlways, colorNever:
		eff.Color = ms
		r.PrefColorSet = true
		return nil
	default:
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgColorMode, path, jq.Str(m))}
	}
}

// validatePersonalSections validates the review/spend/privacy sections
// of the settings file through the typed personal parser, so the
// loader and the fetch-time personal read share one contract. A
// section violation refuses with its typed configuration code.
func validatePersonalSections(obj map[string]any) error {
	if ref := config.ValidatePersonalSections(obj); ref != nil {
		return &RefusalError{Code: ref.Code, Message: ref.Message}
	}
	return nil
}

func countCredKeys(doc any) int {
	w := &credWalker{}
	w.walk(doc)
	return w.n
}

type credWalker struct {
	n int
}

func (w *credWalker) walk(v any) {
	switch t := v.(type) {
	case map[string]any:
		w.walkMap(t)
	case []any:
		for _, child := range t {
			w.walk(child)
		}
	}
}

func (w *credWalker) walkMap(t map[string]any) {
	for k, child := range t {
		if credRe.MatchString(readcommon.ASCIILower(k)) {
			w.n++
		}
		w.walk(child)
	}
}
