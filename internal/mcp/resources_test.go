package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core/skill"
)

const resourcePrefix = "verdict://skill/"

var literalMIME = map[string]string{
	"PROCEDURE.md":               "text/markdown",
	"schemas/review.schema.json": "application/json",
	"scripts/verdict":            "text/plain",
}

type resourceDescriptorFrame struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
}

type resourceListResult struct {
	Resources []resourceDescriptorFrame `json:"resources"`
}

type resourceReadResult struct {
	Contents []struct {
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType"`
		Text     string `json:"text"`
	} `json:"contents"`
}

// TestResourcesListInventory pins one descriptor per served payload path.
func TestResourcesListInventory(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	if frames["1"].Error != nil {
		t.Fatalf("resources/list error = %+v", frames["1"].Error)
	}
	var result resourceListResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatal(err)
	}
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != len(paths) {
		t.Fatalf("resources = %d, want the %d served paths", len(result.Resources), len(paths))
	}
	for i, path := range paths {
		checkResourceDescriptor(t, i, path, result.Resources[i])
	}
	checkLiteralMIMEs(t, result.Resources)
}

func checkResourceDescriptor(t *testing.T, i int, path string, got resourceDescriptorFrame) {
	t.Helper()
	if want := resourcePrefix + path; got.URI != want {
		t.Errorf("resource %d uri = %q, want %q", i, got.URI, want)
	}
	if got.Name != path {
		t.Errorf("resource %d name = %q, want %q", i, got.Name, path)
	}
	if want := literalMIMEFor(path); got.MIMEType != want {
		t.Errorf("%s mimeType = %q, want %q", path, got.MIMEType, want)
	}
}

func checkLiteralMIMEs(t *testing.T, resources []resourceDescriptorFrame) {
	t.Helper()
	for path, want := range literalMIME {
		got, found := resourceNamed(resources, path)
		if !found {
			t.Errorf("resources/list misses literal MIME case %q", path)
			continue
		}
		if got.MIMEType != want {
			t.Errorf("%s mimeType = %q, want literal %q", path, got.MIMEType, want)
		}
	}
}

func resourceNamed(resources []resourceDescriptorFrame, path string) (resourceDescriptorFrame, bool) {
	for _, resource := range resources {
		if resource.Name == path {
			return resource, true
		}
	}
	return resourceDescriptorFrame{}, false
}

// TestResourcesReadMatchesSkillShow proves every served path reads back the
// same bytes and mime type the skill service gives `verdict skill show`.
func TestResourcesReadMatchesSkillShow(t *testing.T) {
	t.Parallel()
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	frames := serveResourceReads(t, paths)
	for i, path := range paths {
		checkResourceRead(t, path, frames[strconv.Itoa(i+1)])
	}
}

func serveResourceReads(t *testing.T, paths []string) map[string]frame {
	t.Helper()
	lines := make([]string, 0, len(paths))
	for i, path := range paths {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`,
			i+1, resourcePrefix+path))
	}
	cfg := testConfig()
	return serve(t, &cfg, lines...)
}

func checkResourceRead(t *testing.T, path string, got frame) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("resources/read %s error = %+v", path, got.Error)
	}
	var result resourceReadResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("%s contents = %d, want one", path, len(result.Contents))
	}
	content := result.Contents[0]
	if content.URI != resourcePrefix+path || content.MIMEType != literalMIMEFor(path) {
		t.Errorf("%s content = %+v, want its URI and mime type", path, content)
	}
	want, ref := skill.ShowFile(context.Background(), &skill.Request{}, path)
	if ref != nil {
		t.Fatalf("skill show %s: %v", path, ref)
	}
	if !bytes.Equal([]byte(content.Text), want) {
		t.Errorf("resources/read %s differs from skill show bytes", path)
	}
}

// TestResourcesReadRefusesBadURIs pins -32002 for unknown, foreign, traversal,
// absolute, and empty paths. The URI appears in every message.
func TestResourcesReadRefusesBadURIs(t *testing.T) {
	t.Parallel()
	uris := []string{
		resourcePrefix + "nope.md",
		"file:///etc/passwd",
		resourcePrefix + "../PROCEDURE.md",
		resourcePrefix + "prompts/../../PROCEDURE.md",
		resourcePrefix + "/PROCEDURE.md",
		resourcePrefix + "%2e%2e/PROCEDURE.md",
		resourcePrefix,
	}
	lines := make([]string, 0, len(uris))
	for i, uri := range uris {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`, i+1, uri))
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for i, uri := range uris {
		checkResourceRefusal(t, uri, frames[strconv.Itoa(i+1)])
	}
}

func checkResourceRefusal(t *testing.T, uri string, got frame) {
	t.Helper()
	if got.Error == nil || got.Error.Code != codeResourceNotFound {
		t.Fatalf("resources/read %q = %+v, want -32002", uri, got.Error)
	}
	if !strings.Contains(got.Error.Message, uri) {
		t.Errorf("resources/read %q message = %q, want the URI", uri, got.Error.Message)
	}
}

func literalMIMEFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".md"):
		return "text/markdown"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	default:
		return "text/plain"
	}
}
