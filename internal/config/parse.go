package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
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

// Closed vocabularies the semantic pass enforces.
var (
	categoryEnum = []string{"logic", "edge-case", "security", "performance", "accidental-commit", "style", "docs", "other"}
	checkEnum    = []string{"no-open-above-block", "head-current", "no-live-threads"}
	severityEnum = []string{"critical", "important", "informational"}
	effortEnum   = []string{"cheap", "standard", "deep"}
	depthEnum    = []string{"standard", "deep"}
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

// msgMalformedJSON names the byte offset of invalid JSON input.
const msgMalformedJSON = "%s: malformed JSON at offset %d"

// decodeStrict decodes with duplicate detection and trailing-data refusal.
// The returned tree holds map[string]any, []any, string, [json.Number],
// bool, and nil only.
func decodeStrict(raw []byte) (any, *core.RefusalError) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, refuse(CodeBadConfig, "$: malformed JSON at offset %d", dec.InputOffset())
	}
	val, ref := readTokenValue(dec, tok, "$")
	if ref != nil {
		return nil, ref
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, refuse(CodeBadConfig, "$: trailing data after the top object at offset %d", dec.InputOffset())
	}
	return val, nil
}

// readTokenValue converts one already-read token into the generic tree.
func readTokenValue(dec *json.Decoder, tok json.Token, path string) (any, *core.RefusalError) {
	if delim, ok := tok.(json.Delim); ok {
		switch delim {
		case '{':
			return readObject(dec, path)
		case '[':
			return readArray(dec, path)
		default:
			return nil, refuse(CodeBadConfig, msgMalformedJSON, path, dec.InputOffset())
		}
	}
	return tok, nil
}

// readObject reads one JSON object, refusing duplicate keys naming the
// repeated key and its path.
func readObject(dec *json.Decoder, path string) (any, *core.RefusalError) {
	obj := map[string]any{}
	for dec.More() {
		if ref := readObjectEntry(dec, obj, path); ref != nil {
			return nil, ref
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, refuse(CodeBadConfig, msgMalformedJSON, path, dec.InputOffset())
	}
	return obj, nil
}

// readObjectEntry reads one key-value pair into the object, refusing
// duplicates naming the repeated key.
func readObjectEntry(dec *json.Decoder, obj map[string]any, path string) *core.RefusalError {
	key, ref := readObjectKey(dec, path)
	if ref != nil {
		return ref
	}
	if _, dup := obj[key]; dup {
		return refuse(CodeBadConfig, "%s: duplicate key %q", path, key)
	}
	val, ref := readObjectValue(dec, path, key)
	if ref != nil {
		return ref
	}
	obj[key] = val
	return nil
}

// readObjectKey reads one object key, which must be a string.
func readObjectKey(dec *json.Decoder, path string) (string, *core.RefusalError) {
	keyTok, err := dec.Token()
	if err != nil {
		return "", refuse(CodeBadConfig, msgMalformedJSON, path, dec.InputOffset())
	}
	key, ok := keyTok.(string)
	if !ok {
		return "", refuse(CodeBadConfig, msgMalformedJSON, path, dec.InputOffset())
	}
	return key, nil
}

// readObjectValue reads the value following one object key.
func readObjectValue(dec *json.Decoder, path, key string) (any, *core.RefusalError) {
	valTok, err := dec.Token()
	if err != nil {
		return nil, refuse(CodeBadConfig, "%s."+key+": malformed JSON at offset %d", path, dec.InputOffset())
	}
	return readTokenValue(dec, valTok, path+"."+key)
}

// readArray reads one JSON array, threading the index into child paths.
func readArray(dec *json.Decoder, path string) (any, *core.RefusalError) {
	var out []any
	for i := 0; dec.More(); i++ {
		valTok, err := dec.Token()
		if err != nil {
			return nil, refuse(CodeBadConfig, "%s[%d]: malformed JSON at offset %d", path, i, dec.InputOffset())
		}
		val, ref := readTokenValue(dec, valTok, fmt.Sprintf("%s[%d]", path, i))
		if ref != nil {
			return nil, ref
		}
		out = append(out, val)
	}
	if _, err := dec.Token(); err != nil {
		return nil, refuse(CodeBadConfig, msgMalformedJSON, path, dec.InputOffset())
	}
	return out, nil
}

// jsonKind names a decoded value for diagnostics.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}

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
