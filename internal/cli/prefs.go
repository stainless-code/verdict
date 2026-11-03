package cli

import (
	"fmt"
	"path/filepath"
	"regexp"

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

// ResolvePrefs ports resolve_prefs + resolve_publish + resolve_publish_diagrams
// in order: env layer, then the settings file layer. Warnings mirror the
// shell's warn lines; file problems return typed refusals. readFile injects
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

// resolveOutputEnv ports the REVIEW_OUTPUT_FORMAT knob.
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

// resolveCompactEnv ports the REVIEW_LAYOUT_COMPACT knob.
func resolveCompactEnv(env map[string]string, eff *Effective, warn func(string)) {
	switch c := env["REVIEW_LAYOUT_COMPACT"]; c {
	case "":
	case FlagOn, FlagOff:
		eff.Compact = c
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_LAYOUT_COMPACT '%s' (want true|false)", c))
	}
}

// resolveColorEnv ports the REVIEW_COLOR_MODE knob.
func resolveColorEnv(env map[string]string, eff *Effective, warn func(string)) {
	switch c := env["REVIEW_COLOR_MODE"]; c {
	case "":
	case colorAuto, colorAlways, colorNever:
		eff.Color = c
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_COLOR_MODE '%s' (want auto|always|never)", c))
	}
}

// gateMissingFile handles the absent-settings path: the attribution-text env
// knob still gates before defaults win.
func gateMissingFile(eff *Effective, path string) (*PrefsResult, error) {
	if ref := ValidateAttributionPlaceholders(eff.PublishAttributionText, "publish.attribution_text"); ref != nil {
		return nil, ref
	}
	return &PrefsResult{Effective: *eff, Path: path}, nil
}

// resolveSettingsFile validates the decoded settings object section by
// section.
func resolveSettingsFile(r *Runner, path string, obj map[string]any, doc any, eff *Effective) (*PrefsResult, error) {
	if n := countCredKeys(doc); n > 0 {
		return nil, &RefusalError{Code: "bad-settings-credentials", Message: fmt.Sprintf(msgCredKey, path)}
	}
	if ref := checkTopKeys(path, obj); ref != nil {
		return nil, ref
	}
	if ref := checkSchemaKey(path, obj); ref != nil {
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
	return &PrefsResult{Effective: *eff, Path: path}, nil
}

// checkTopKeys rejects unknown top-level settings keys.
func checkTopKeys(path string, obj map[string]any) error {
	for _, k := range SortedKeys(obj) {
		switch k {
		case "$schema", "output", "layout", "color", "publish":
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgTopKey, path, k)}
		}
	}
	return nil
}

// checkSchemaKey validates the $schema hint key.
func checkSchemaKey(path string, obj map[string]any) error {
	if _, ok := obj["$schema"]; ok {
		if _, isStr := obj["$schema"].(string); !isStr {
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgSchemaType, path)}
		}
	}
	return nil
}

// resolveOutputFile ports the output.format file knob.
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

// applyOutputFormat applies the output.format value.
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

// resolveLayoutFile ports the layout.compact file knob.
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

// applyCompact applies the layout.compact value.
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

// resolveColorFile ports the color.mode file knob.
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

// applyColorMode applies the color.mode value.
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

// countCredKeys counts object keys anywhere in doc whose ASCII-lowercased
// form matches the credential pattern, mirroring the shell's
// `[.. | objects | keys[] | ascii_downcase] | map(select(test(...)))`.
func countCredKeys(doc any) int {
	w := &credWalker{}
	w.walk(doc)
	return w.n
}

// credWalker counts credential-like keys anywhere in the settings document.
type credWalker struct {
	n int
}

// walk counts credential keys under v.
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

// walkMap counts credential keys in one object.
func (w *credWalker) walkMap(t map[string]any) {
	for k, child := range t {
		if credRe.MatchString(readcommon.ASCIILower(k)) {
			w.n++
		}
		w.walk(child)
	}
}
