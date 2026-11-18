package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const bootstrapByteLimit = 2048

var (
	bootstrapCommandVerb = regexp.MustCompile("`verdict ([a-z][a-z-]*)(?:[ `]|$)")
	bootstrapForbidden   = regexp.MustCompile(`(?i)\b(?:severity|rigor|confidence|anchor|chunks)\b`)
	bootstrapBackticked  = regexp.MustCompile("`([a-z][a-z0-9_.-]*)`")
	bootstrapCompound    = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:[-_][a-z0-9]+)+\b`)
)

// TestBootstrapSkillStaysThin keeps the installed skill a stable router. The
// size cap, frontmatter keys, command mentions, and absent Procedure doctrine
// are all checked against the file itself; the schema-name check looks for
// code-formed property tokens (backticked or snake/kebab cased), so ordinary
// prose words such as "file" and "<path>" are not mistaken for schema fields.
func TestBootstrapSkillStaysThin(t *testing.T) {
	t.Parallel()
	raw, frontmatter, body := bootstrapSkillParts(t)
	checks := []struct {
		run  func(*testing.T, []byte, map[string]string, string)
		name string
	}{
		{checkBootstrapSize, "size"},
		{checkBootstrapFrontmatter, "frontmatter"},
		{checkBootstrapVerbs, "verbs"},
		{checkBootstrapDoctrine, "doctrine"},
		{checkBootstrapSchemaNames, "schema properties"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			check.run(t, raw, frontmatter, body)
		})
	}
}

func bootstrapSkillParts(t *testing.T) ([]byte, map[string]string, string) {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "skills", "verdict", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("%s has no frontmatter", path)
	}
	front, body, ok := strings.Cut(text[len("---\n"):], "\n---\n")
	if !ok {
		t.Fatalf("%s has no frontmatter closing fence", path)
	}
	return raw, bootstrapFields(front), strings.TrimPrefix(body, "\n")
}

func bootstrapFields(front string) map[string]string {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(front, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return fields
}

func checkBootstrapSize(t *testing.T, raw []byte, _ map[string]string, _ string) {
	t.Helper()
	if len(raw) > bootstrapByteLimit {
		t.Errorf("SKILL.md is %d bytes, limit %d", len(raw), bootstrapByteLimit)
	}
}

func checkBootstrapFrontmatter(t *testing.T, _ []byte, fields map[string]string, _ string) {
	t.Helper()
	for _, key := range []string{"name", "description"} {
		if fields[key] == "" {
			t.Errorf("frontmatter has no %s", key)
		}
	}
	if _, err := strconv.Atoi(fields["stub_version"]); err != nil {
		t.Errorf("frontmatter stub_version = %q, want an integer: %v", fields["stub_version"], err)
	}
}

func checkBootstrapVerbs(t *testing.T, _ []byte, _ map[string]string, body string) {
	t.Helper()
	allowed := map[string]bool{"skill": true, "post": true}
	for _, match := range bootstrapCommandVerb.FindAllStringSubmatch(body, -1) {
		if !allowed[match[1]] {
			t.Errorf("SKILL.md names verdict %s, only skill and post are allowed", match[1])
		}
	}
}

func checkBootstrapDoctrine(t *testing.T, _ []byte, _ map[string]string, body string) {
	t.Helper()
	for _, word := range bootstrapForbidden.FindAllString(body, -1) {
		t.Errorf("SKILL.md carries Procedure doctrine word %q", word)
	}
}

func checkBootstrapSchemaNames(t *testing.T, _ []byte, _ map[string]string, body string) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(readSchemaFile(t, "review.schema.json"), &doc); err != nil {
		t.Fatalf("decode review.schema.json: %v", err)
	}
	properties := make(map[string]bool)
	collectSchemaProperties(doc, properties)
	reportSchemaPropertyTokens(t, strings.ToLower(body), properties)
}

func reportSchemaPropertyTokens(t *testing.T, lower string, properties map[string]bool) {
	t.Helper()
	seen := make(map[string]bool)
	for _, pattern := range []*regexp.Regexp{bootstrapBackticked, bootstrapCompound} {
		for _, token := range pattern.FindAllString(lower, -1) {
			reportSchemaToken(t, strings.Trim(token, "`"), properties, seen)
		}
	}
}

func reportSchemaToken(t *testing.T, token string, properties, seen map[string]bool) {
	t.Helper()
	if !properties[token] || seen[token] {
		return
	}
	seen[token] = true
	t.Errorf("SKILL.md names review.schema.json property %q", token)
}

func collectSchemaProperties(node any, out map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		collectSchemaMap(value, out)
	case []any:
		collectSchemaSlice(value, out)
	}
}

func collectSchemaMap(node map[string]any, out map[string]bool) {
	if props, ok := node["properties"].(map[string]any); ok {
		for name := range props {
			out[name] = true
		}
	}
	for _, child := range node {
		collectSchemaProperties(child, out)
	}
}

func collectSchemaSlice(node []any, out map[string]bool) {
	for _, child := range node {
		collectSchemaProperties(child, out)
	}
}
