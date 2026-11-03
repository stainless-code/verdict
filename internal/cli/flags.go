package cli

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// GlobalForces is core's flag-layer override set (alias of
// core.GlobalForces).
type GlobalForces = core.GlobalForces

// ApplyGlobalForces applies the flag layer over eff and derives the final
// JSONMode, mirroring lines 973-997 of the shell.
func ApplyGlobalForces(r *Runner, g *GlobalForces, noColorSet bool) error {
	eff := &r.Effective
	applyStringForces(r, eff, g)
	if ref := ValidateAttributionPlaceholders(eff.PublishAttributionText, "publish.attribution_text"); ref != nil {
		return ref
	}
	applyColorFallback(r, eff, g, noColorSet)
	applyJSONMode(r, g)
	return nil
}

// forceOverlay maps one force string onto its destination.
type forceOverlay struct {
	dst   *string
	force string
}

// applyStringForces overlays the non-empty flag layer onto the runner.
func applyStringForces(r *Runner, eff *Effective, g *GlobalForces) {
	if g.OutputForce != "" {
		r.Output = g.OutputForce
	}
	overlay := []forceOverlay{
		{dst: &eff.Compact, force: g.CompactForce},
		{dst: &eff.Color, force: g.ColorForce},
		{dst: &eff.PublishFooter, force: g.PublishFooterForce},
		{dst: &eff.PublishAttribution, force: g.PublishAttributionForce},
		{dst: &eff.PublishLogo, force: g.PublishLogoForce},
		{dst: &eff.PublishOverview, force: g.PublishOverviewForce},
		{dst: &eff.DiagramsStructural, force: g.DiagramsStructuralForce},
		{dst: &eff.DiagramsBehavioral, force: g.DiagramsBehavioralForce},
		{dst: &eff.DiagramsExpanded, force: g.DiagramsExpandedForce},
	}
	for _, o := range overlay {
		if o.force != "" {
			*o.dst = o.force
		}
	}
	if g.PublishAttributionTextSet {
		eff.PublishAttributionText = g.PublishAttributionText
	}
	if g.PublishCopyrightSet {
		eff.PublishCopyright = g.PublishCopyright
	}
	if g.PublishLogoURLSet {
		eff.PublishLogoURL = g.PublishLogoURLForce
	}
}

// applyColorFallback ports the no-color rule: an explicit --no-color wins
// unless the color mode is forced or configured.
func applyColorFallback(r *Runner, eff *Effective, g *GlobalForces, noColorSet bool) {
	if noColorSet && g.ColorForce != colorAlways && r.EnvReviewColorMode == "" && !r.PrefColorSet {
		eff.Color = colorNever
	}
}

// applyJSONMode derives JSONMode from the output layer.
func applyJSONMode(r *Runner, g *GlobalForces) {
	if g.OutputForce == formatText {
		r.JSONMode = false
	} else if r.Output == formatJSON {
		r.JSONMode = true
	}
}

// StripGlobal removes the global presentation flags, returning the remaining
// args. Unknown flags pass through for the verb parser. A --json anywhere
// before `--` is peeked for the prefs phase. Missing option values are plain
// usage errors (exit 2, no envelope); invalid values are typed refusals.
func StripGlobal(args []string) (rest []string, g *GlobalForces, uerr *UsageError, ref error) {
	g = &GlobalForces{}
	st := &stripState{args: args, g: g}
	for st.i < len(st.args) {
		r, u, done, f := st.step()
		if u != nil || f != nil {
			return nil, nil, u, f
		}
		if done {
			continue
		}
		rest = append(rest, r...)
	}
	return rest, g, nil, nil
}

// stripState carries the StripGlobal scan position.
type stripState struct {
	g    *GlobalForces
	args []string
	i    int
	seen bool
}

// step consumes one arg, returning passthrough rest and whether the arg was
// consumed as a flag.
func (st *stripState) step() ([]string, *UsageError, bool, error) {
	a := st.args[st.i]
	if st.seen {
		st.i++
		return []string{a}, nil, false, nil
	}
	consumed, r, u, f := stripOne(st)
	if u != nil || f != nil {
		return nil, u, true, f
	}
	st.i += max(consumed, 1)
	if consumed == 0 {
		if set, ok := matchToggle(a); ok {
			set(st.g)
			return nil, nil, true, nil
		}
		return []string{a}, nil, false, nil
	}
	return r, nil, false, nil
}

// boolFlags maps boolean global flags to their setters.
var boolFlags = map[string]func(*GlobalForces){
	"--compact":               func(g *GlobalForces) { g.CompactForce = FlagOn },
	"--no-compact":            func(g *GlobalForces) { g.CompactForce = FlagOff },
	"--footer":                func(g *GlobalForces) { g.PublishFooterForce = FlagOn },
	"--no-footer":             func(g *GlobalForces) { g.PublishFooterForce = FlagOff },
	"--overview":              func(g *GlobalForces) { g.PublishOverviewForce = FlagOn },
	"--no-overview":           func(g *GlobalForces) { g.PublishOverviewForce = FlagOff },
	"--diagram-behavioral":    func(g *GlobalForces) { g.DiagramsBehavioralForce = FlagOn },
	"--no-diagram-behavioral": func(g *GlobalForces) { g.DiagramsBehavioralForce = FlagOff },
	"--diagram-structural":    func(g *GlobalForces) { g.DiagramsStructuralForce = FlagOn },
	"--no-diagram-structural": func(g *GlobalForces) { g.DiagramsStructuralForce = FlagOff },
	"--diagram-expanded":      func(g *GlobalForces) { g.DiagramsExpandedForce = FlagOn },
	"--no-diagram-expanded":   func(g *GlobalForces) { g.DiagramsExpandedForce = FlagOff },
	"--logo":                  func(g *GlobalForces) { g.PublishLogoForce = FlagOn },
	"--no-logo":               func(g *GlobalForces) { g.PublishLogoForce = FlagOff },
	"--json":                  func(g *GlobalForces) { g.OutputForce = formatJSON },
	"--no-json":               func(g *GlobalForces) { g.OutputForce = formatText },
	"--no-attribution":        func(g *GlobalForces) { g.PublishAttributionForce = AttributionOff },
}

// matchToggle matches a boolean global flag.
func matchToggle(a string) (func(*GlobalForces), bool) {
	set, ok := boolFlags[a]
	return set, ok
}

// stripOne consumes one value-taking global flag at the scan position. It
// returns the consumed arg count, passthrough rest args, and any
// usage/refusal error; a zero count means a is not a value flag (boolean
// toggles and passthrough are handled by the caller).
func stripOne(st *stripState) (int, []string, *UsageError, error) {
	args, i, g := st.args, st.i, st.g
	a := args[i]
	switch {
	case a == "--":
		st.seen = true
		return 1, []string{a}, nil, nil
	case a == "--color":
		return stripColor(args, i, g)
	case a == "--attribution":
		return stripAttribution(args, i, g)
	case a == "--logo-url":
		return stripLogoURL(args, i, g)
	case a == "--attribution-text":
		return stripTextValue(st, "<text>", setAttributionText)
	case a == "--copyright":
		return stripTextValue(st, "<text>", setCopyright)
	case strings.Contains(a, "="):
		return stripEq(a, g)
	default:
		return 0, nil, nil, nil
	}
}

// setLogoURL records a logo URL force.
func setLogoURL(v string, g *GlobalForces) {
	g.PublishLogoURLForce = v
	g.PublishLogoURLSet = true
}

// setAttributionText records an attribution text force.
func setAttributionText(v string, g *GlobalForces) {
	g.PublishAttributionText = v
	g.PublishAttributionTextSet = true
}

// setCopyright records a copyright force.
func setCopyright(v string, g *GlobalForces) {
	g.PublishCopyright = v
	g.PublishCopyrightSet = true
}

// MissingValue is the one missing-value usage error: a flag that was given
// without its value is a known flag, never an unknown one. want names the
// accepted values and is appended as `(want ...)`; "" omits it.
func MissingValue(flag, want string) *UsageError {
	msg := flag + " needs a value"
	if want != "" {
		msg += " (want " + want + ")"
	}
	return &UsageError{Message: msg}
}

// stripColor consumes the --color flag.
func stripColor(args []string, i int, g *GlobalForces) (int, []string, *UsageError, error) {
	if i+1 >= len(args) {
		return 0, nil, MissingValue(args[i], "auto|always|never"), nil
	}
	if ref := setColor(args[i+1], g); ref != nil {
		return 0, nil, nil, ref
	}
	return 2, nil, nil, nil
}

// setColor validates one color value.
func setColor(v string, g *GlobalForces) error {
	switch v {
	case colorAuto, colorAlways, colorNever:
		g.ColorForce = v
		return nil
	default:
		return &RefusalError{Code: "bad-color", Message: fmt.Sprintf("unknown --color '%s' (want auto|always|never)", v)}
	}
}

// stripAttributionEq consumes the --attribution=value form.
func stripAttributionEq(a string, g *GlobalForces) (int, []string, *UsageError, error) {
	v := strings.TrimPrefix(a, "--attribution=")
	if !isAttributionTier(v) {
		return 0, nil, nil, &RefusalError{Code: "bad-attribution", Message: fmt.Sprintf(
			"unknown --attribution '%s' (want off|product|full)", v)}
	}
	g.PublishAttributionForce = v
	return 1, nil, nil, nil
}

// stripLogoURL consumes the --logo-url value flag.
func stripLogoURL(args []string, i int, g *GlobalForces) (int, []string, *UsageError, error) {
	if i+1 >= len(args) {
		return 0, nil, MissingValue(args[i], "<url>"), nil
	}
	setLogoURL(args[i+1], g)
	return 2, nil, nil, nil
}

// stripAttribution consumes the --attribution flag (bare means product).
func stripAttribution(args []string, i int, g *GlobalForces) (int, []string, *UsageError, error) {
	if i+1 < len(args) && isAttributionTier(args[i+1]) {
		g.PublishAttributionForce = args[i+1]
		return 2, nil, nil, nil
	}
	g.PublishAttributionForce = AttributionProduct
	return 1, nil, nil, nil
}

// isAttributionTier reports whether v is a valid attribution tier.
func isAttributionTier(v string) bool {
	switch v {
	case AttributionOff, AttributionProduct, AttributionFull:
		return true
	default:
		return false
	}
}

// PeekJSON reports whether --json appears before any `--` separator,
// mirroring the shell's early peek.
func PeekJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" {
			return true
		}
	}
	return false
}

// stripTextValue consumes a --flag <text> value flag.
func stripTextValue(st *stripState, want string, set func(string, *GlobalForces)) (int, []string, *UsageError, error) {
	args, i := st.args, st.i
	a := args[i]
	if i+1 >= len(args) {
		return 0, nil, MissingValue(a, want), nil
	}
	set(args[i+1], st.g)
	return 2, nil, nil, nil
}

// stripEq consumes the --flag=value forms.
func stripEq(a string, g *GlobalForces) (int, []string, *UsageError, error) {
	switch {
	case strings.HasPrefix(a, "--color="):
		return 1, nil, nil, setColor(strings.TrimPrefix(a, "--color="), g)
	case strings.HasPrefix(a, "--attribution="):
		return stripAttributionEq(a, g)
	case strings.HasPrefix(a, "--logo-url="):
		setLogoURL(strings.TrimPrefix(a, "--logo-url="), g)
		return 1, nil, nil, nil
	case strings.HasPrefix(a, "--attribution-text="):
		setAttributionText(strings.TrimPrefix(a, "--attribution-text="), g)
		return 1, nil, nil, nil
	case strings.HasPrefix(a, "--copyright="):
		setCopyright(strings.TrimPrefix(a, "--copyright="), g)
		return 1, nil, nil, nil
	default:
		return 0, nil, nil, nil
	}
}
