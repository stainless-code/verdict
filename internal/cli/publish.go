package cli

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// resolvePublishEnv owns the env half of resolve_publish: flags > file >
// env > defaults, so the env layer applies even with no settings file. It
// only warns; invalid values fall back to defaults.
func resolvePublishEnv(env map[string]string, eff *Effective, warn func(string)) {
	resolveOnOffEnv(env, "REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL", &eff.DiagramsStructural, warn)
	resolveOnOffEnv(env, "REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL", &eff.DiagramsBehavioral, warn)
	resolveOnOffEnv(env, "REVIEW_PUBLISH_DIAGRAMS_EXPANDED", &eff.DiagramsExpanded, warn)
	resolveOnOffEnv(env, "REVIEW_PUBLISH_FOOTER", &eff.PublishFooter, warn)
	resolveAttributionEnv(env, eff, warn)
	if v := env["REVIEW_PUBLISH_ATTRIBUTION_TEXT"]; v != "" {
		eff.PublishAttributionText = v
	}
	if v := env["REVIEW_PUBLISH_COPYRIGHT"]; v != "" {
		eff.PublishCopyright = v
	}
	resolveOnOffEnv(env, "REVIEW_PUBLISH_LOGO", &eff.PublishLogo, warn)
	if v := env["REVIEW_PUBLISH_LOGO_URL"]; v != "" {
		eff.PublishLogoURL = v
	}
	resolveOnOffEnv(env, "REVIEW_PUBLISH_OVERVIEW", &eff.PublishOverview, warn)
}

// resolveOnOffEnv handles one true|false env knob.
func resolveOnOffEnv(env map[string]string, key string, dst *string, warn func(string)) {
	switch v := env[key]; v {
	case "":
	case FlagOn, FlagOff:
		*dst = v
	default:
		warn(fmt.Sprintf("ignoring invalid %s '%s' (want true|false)", key, v))
	}
}

// resolveAttributionEnv owns the REVIEW_PUBLISH_ATTRIBUTION knob with its
// P1-bool migration.
func resolveAttributionEnv(env map[string]string, eff *Effective, warn func(string)) {
	switch v := env["REVIEW_PUBLISH_ATTRIBUTION"]; v {
	case "":
	case AttributionOff, AttributionProduct, AttributionFull:
		eff.PublishAttribution = v
	case FlagOn:
		warn("REVIEW_PUBLISH_ATTRIBUTION 'true' is deprecated (P1 bool migrated to off|product|full) — treating as product")
		eff.PublishAttribution = AttributionProduct
	case FlagOff:
		warn("REVIEW_PUBLISH_ATTRIBUTION 'false' is deprecated (P1 bool migrated to off|product|full) — treating as off")
		eff.PublishAttribution = AttributionOff
	default:
		warn(fmt.Sprintf("ignoring invalid REVIEW_PUBLISH_ATTRIBUTION '%s' (want off|product|full)", v))
	}
}

// resolvePublishFile owns the settings-file half of resolve_publish plus
// resolve_publish_diagrams. A missing file or a file without a publish key
// keeps the env layer and only validates the attribution placeholders.
func resolvePublishFile(path string, obj map[string]any, eff *Effective) error {
	raw, hasPublish := obj["publish"]
	if !hasPublish {
		return validateAttributionText(path, eff)
	}
	pub, ok := jq.AsMap(raw)
	if !ok {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgPublishObj, path)}
	}
	if ref := checkPublishKeys(path, pub); ref != nil {
		return ref
	}
	return resolvePublishKeys(path, pub, eff)
}

// publishKey maps one scalar publish key to its destination.
type publishKey struct {
	dst  *string
	key  string
	hint string
	bool bool
}

// publishKeys lists the scalar publish keys with their destinations.
func publishKeys(eff *Effective) []publishKey {
	return []publishKey{
		{dst: &eff.PublishAttributionText, key: "attribution_text"},
		{dst: &eff.PublishCopyright, key: "copyright"},
		{dst: &eff.PublishLogo, key: "logo", bool: true},
		{dst: &eff.PublishLogoURL, key: "logo_url", hint: " (empty reads text-only)"},
		{dst: &eff.PublishOverview, key: "overview", bool: true},
	}
}

// resolvePublishKeys applies the scalar publish keys, then diagrams.
func resolvePublishKeys(path string, pub map[string]any, eff *Effective) error {
	for _, step := range publishSteps(path, pub, eff) {
		if ref := step(); ref != nil {
			return ref
		}
	}
	if raw, ok := pub["diagrams"]; ok {
		if ref := resolveDiagrams(path, raw, eff); ref != nil {
			return ref
		}
	}
	return validateAttributionText(path, eff)
}

// publishSteps lists the scalar publish-key appliers in shell order.
func publishSteps(path string, pub map[string]any, eff *Effective) []func() error {
	return []func() error{
		func() error { return resolveFooterKey(path, pub, eff) },
		func() error { return resolveAttributionKey(path, pub, eff) },
		func() error { return applyPublishKeys(path, pub, eff) },
	}
}

// applyPublishKeys applies every scalar publish key in the table.
func applyPublishKeys(path string, pub map[string]any, eff *Effective) error {
	for _, k := range publishKeys(eff) {
		if ref := applyPublishKey(path, pub, k); ref != nil {
			return ref
		}
	}
	return nil
}

// applyPublishKey applies one scalar publish key.
func applyPublishKey(path string, pub map[string]any, k publishKey) error {
	if k.bool {
		return resolveBoolKey(path, pub, k.key, k.dst)
	}
	return resolveTextKey(path, pub, k.key, k.hint, k.dst)
}

// validateAttributionText gates the attribution_text placeholders.
func validateAttributionText(path string, eff *Effective) error {
	return ValidateAttributionPlaceholders(eff.PublishAttributionText, "settings file "+path+": publish.attribution_text")
}

// checkPublishKeys rejects unknown publish keys.
func checkPublishKeys(path string, pub map[string]any) error {
	for _, k := range SortedKeys(pub) {
		switch k {
		case "footer", "attribution", "attribution_text", "copyright", "logo", "logo_url", "overview", "diagrams":
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgPublishKeys, path, k)}
		}
	}
	return nil
}

// publishBoolKey reads one boolean publish key.
func publishBoolKey(path string, pub map[string]any, key, want string) (string, error) {
	raw, ok := pub[key]
	if !ok {
		return "", nil
	}
	b, isBool := raw.(bool)
	if !isBool {
		return "", &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgPublishBool, path, key, want)}
	}
	if b {
		return FlagOn, nil
	}
	return FlagOff, nil
}

// resolveBoolKey applies one boolean publish key to its destination.
func resolveBoolKey(path string, pub map[string]any, key string, dst *string) error {
	if _, ok := pub[key]; !ok {
		return nil
	}
	s, ref := publishBoolKey(path, pub, key, "true|false")
	if ref != nil {
		return ref
	}
	*dst = s
	return nil
}

// resolveFooterKey applies the publish.footer key.
func resolveFooterKey(path string, pub map[string]any, eff *Effective) error {
	if _, ok := pub["footer"]; !ok {
		return nil
	}
	s, ref := publishBoolKey(path, pub, "footer", "true|false")
	if ref != nil {
		return ref
	}
	eff.PublishFooter = s
	return nil
}

// resolveAttributionKey applies the publish.attribution tier key.
func resolveAttributionKey(path string, pub map[string]any, eff *Effective) error {
	raw, ok := pub["attribution"]
	if !ok {
		return nil
	}
	switch t := raw.(type) {
	case bool:
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgAttrMigrated, path)}
	case string:
		switch t {
		case AttributionOff, AttributionProduct, AttributionFull:
			eff.PublishAttribution = t
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgAttrUnknown, path, t)}
		}
	default:
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgAttrType, path)}
	}
	return nil
}

// resolveTextKey applies one string publish key to its destination; hint
// appends to the type error (the logo_url empty-means-text-only note).
func resolveTextKey(path string, pub map[string]any, key, hint string, dst *string) error {
	raw, ok := pub[key]
	if !ok {
		return nil
	}
	s, isStr := raw.(string)
	if !isStr {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgTextType, path, key, hint)}
	}
	*dst = s
	return nil
}

// resolveDiagrams resolves the diagram knobs (flag > file > env).
func resolveDiagrams(path string, raw any, eff *Effective) error {
	d, ok := jq.AsMap(raw)
	if !ok {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgDiagramsObj, path)}
	}
	for _, k := range SortedKeys(d) {
		switch k {
		case "structural", "behavioral", "expanded":
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgDiagramsKey, path, k)}
		}
	}
	for _, kb := range []struct {
		dst *string
		key string
	}{
		{&eff.DiagramsStructural, "structural"},
		{&eff.DiagramsBehavioral, "behavioral"},
		{&eff.DiagramsExpanded, "expanded"},
	} {
		if ref := setDiagramBool(path, d, kb.key, kb.dst); ref != nil {
			return ref
		}
	}
	return nil
}

// setDiagramBool applies one boolean diagrams key.
func setDiagramBool(path string, d map[string]any, key string, dst *string) error {
	raw, ok := d[key]
	if !ok {
		return nil
	}
	b, isBool := raw.(bool)
	if !isBool {
		return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgDiagramsBool, path, key)}
	}
	if b {
		*dst = FlagOn
	} else {
		*dst = FlagOff
	}
	return nil
}

// ValidateAttributionPlaceholders checks the attribution placeholders:
// every {name} placeholder in text must come from the v1 inventory, scanned
// in appearance order. The inventory lives in core so the read path can gate
// overrides without importing this package.
func ValidateAttributionPlaceholders(text, source string) error {
	if ref := core.ValidateAttributionPlaceholders(text, source); ref != nil {
		//nolint:wrapcheck // core's typed refusal is the CLI contract; wrapping would hide its code
		return ref
	}
	return nil
}
