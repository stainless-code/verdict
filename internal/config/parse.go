package config

import (
	"fmt"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/strictjson"
)

// MaxConfigBytes caps one repository configuration: at most 65536 bytes
// are accepted. The D1 snapshot caps apply first (above 256 KiB the file
// refuses as controls-too-large before this parser runs).
const MaxConfigBytes = 65536

// CredentialKeys is the exact published list scanned at every depth:
// any key matching it case-sensitively refuses bad-config-credentials.
// Profile names under `profiles` are user-defined map keys and are exempt.
var CredentialKeys = []string{
	"token", "tokens", "secret", "secrets", "password", "passwd", "pwd",
	"bearer", "credential", "credentials", "api_key", "apikey",
	"client_secret", "private_key", "auth_token", "session_token",
}

const (
	severityCritical      = "critical"
	severityImportant     = "important"
	severityInformational = "informational"
)

// Closed vocabularies the semantic pass enforces.
var (
	categoryEnum = []string{"logic", "edge-case", "security", "performance", "accidental-commit", "style", "docs", "other"}
	checkEnum    = []string{"no-open-above-block", "head-current", "no-live-threads"}
	severityEnum = []string{severityCritical, severityImportant, severityInformational}
	effortEnum   = []string{effortCheap, effortStandard, effortDeep}
	budgetEnum   = []string{"auto", "light", effortStandard, effortDeep}
	depthEnum    = []string{effortStandard, effortDeep}
)

// Parse reads one repository configuration in the fixed refusal order:
// size, JSON shape (malformed, trailing data, duplicate keys, misplaced
// nulls), schema_version, credential scan, unknown fields, then semantic
// validation. One input produces one diagnostic; reads never rewrite.
func Parse(raw []byte) (*core.Config, *core.RefusalError) {
	if len(raw) > MaxConfigBytes {
		return nil, refuse(CodeTooLarge, "configuration is %d bytes, over the %d-byte cap", len(raw), MaxConfigBytes)
	}
	doc, ref := decodeStrict(raw)
	if ref != nil {
		return nil, ref
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "$: want a JSON object, got %s", jsonKind(doc))
	}
	if ref := checkSchemaVersion(root); ref != nil {
		return nil, ref
	}
	if ref := scanCredentials(root); ref != nil {
		return nil, ref
	}
	if ref := checkUnknownFields(root); ref != nil {
		return nil, ref
	}
	return buildConfig(root)
}

// decodeStrict maps the shared decoder's positioned fault to bad-config, so a
// configuration and a recipe refuse through one decoder with their own codes.
func decodeStrict(raw []byte) (any, *core.RefusalError) {
	doc, err := strictjson.Decode(raw)
	if err != nil {
		return nil, refuse(CodeBadConfig, "%s", err)
	}
	return doc, nil
}

// jsonKind renders a decoded value's JSON kind for a diagnostic.
func jsonKind(v any) string { return strictjson.Kind(v) }

// checkSchemaVersion enforces the format marker: the string "1", present.
func checkSchemaVersion(root map[string]any) *core.RefusalError {
	raw, ok := root["schema_version"]
	if !ok {
		return refuse(CodeBadConfig, "schema_version: missing format marker, want the string \"1\"")
	}
	ver, ok := raw.(string)
	if !ok {
		return refuse(CodeBadConfig, "schema_version: want a string, got %s", jsonKind(raw))
	}
	if ver != "1" {
		return refuse(CodeBadConfig, "schema_version: want the string \"1\", got %q", ver)
	}
	return nil
}

// scanCredentials walks every object key against the published list.
// Profile names are user-defined map keys, not fixed-shape fields, so the
// direct keys of `profiles` are exempt; every other key at every depth
// refuses bad-config-credentials naming the key and its path.
func scanCredentials(root map[string]any) *core.RefusalError {
	creds := make(map[string]bool, len(CredentialKeys))
	for _, k := range CredentialKeys {
		creds[k] = true
	}
	return scanCredentialObject(root, "$", creds, false)
}

// scanCredentialObject scans one object and recurses into its values.
// When exemptKeys is set the object's direct keys are user-defined names
// (profile names under `profiles`) and skip the credential check, while
// their values still scan.
func scanCredentialObject(obj map[string]any, path string, creds map[string]bool, exemptKeys bool) *core.RefusalError {
	for _, key := range sortedObjectKeys(obj) {
		childPath := path + "." + key
		if creds[key] && !exemptKeys {
			return refuse(CodeBadCredentials, "%s: credential key %q is never stored in configuration", childPath, key)
		}
		if ref := scanCredentialValue(obj[key], childPath, creds, childPath == "$.profiles"); ref != nil {
			return ref
		}
	}
	return nil
}

// scanCredentialValue recurses into arrays and nested objects. The value of
// the top-level `profiles` key scans with its direct member keys exempt:
// those are profile names, not fixed-shape fields.
func scanCredentialValue(val any, path string, creds map[string]bool, profilesScope bool) *core.RefusalError {
	switch typed := val.(type) {
	case map[string]any:
		return scanCredentialObject(typed, path, creds, profilesScope)
	case []any:
		for i, item := range typed {
			if ref := scanCredentialValue(item, fmt.Sprintf("%s[%d]", path, i), creds, false); ref != nil {
				return ref
			}
		}
	}
	return nil
}

// sortedObjectKeys returns an object's keys in lexical order so the scan
// and every diagnostic are deterministic.
func sortedObjectKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
