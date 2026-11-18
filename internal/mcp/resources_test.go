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

type resourceListResult struct {
	Resources []struct {
		URI      string `json:"uri"`
		Name     string `json:"name"`
		MIMEType string `json:"mimeType"`
	} `json:"resources"`
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
		resource := result.Resources[i]
		if want := resourcePrefix + path; resource.URI != want {
			t.Errorf("resource %d uri = %q, want %q", i, resource.URI, want)
		}
		if resource.Name != path {
			t.Errorf("resource %d name = %q, want %q", i, resource.Name, path)
		}
		if want := literalMIMEFor(path); resource.MIMEType != want {
			t.Errorf("%s mimeType = %q, want %q", path, resource.MIMEType, want)
		}
	}
	for path, want := range literalMIME {
		found := false
		for _, resource := range result.Resources {
			if resource.Name == path {
				found = true
				if resource.MIMEType != want {
					t.Errorf("%s mimeType = %q, want literal %q", path, resource.MIMEType, want)
				}
			}
		}
		if !found {
			t.Errorf("resources/list misses literal MIME case %q", path)
		}
	}
}

// TestResourcesReadMatchesSkillShow proves every served path reads back the
// same bytes and mime type the skill service gives `verdict skill show`.
func TestResourcesReadMatchesSkillShow(t *testing.T) {
	t.Parallel()
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(paths))
	for i, path := range paths {
		lines = append(lines, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%q}}`,
			i+1, resourcePrefix+path))
	}
	cfg := testConfig()
	frames := serve(t, &cfg, lines...)
	for i, path := range paths {
		frame := frames[strconv.Itoa(i+1)]
		if frame.Error != nil {
			t.Fatalf("resources/read %s error = %+v", path, frame.Error)
		}
		var result resourceReadResult
		if err := json.Unmarshal(frame.Result, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("%s contents = %d, want one", path, len(result.Contents))
		}
		content := result.Contents[0]
		wantURI := resourcePrefix + path
		if content.URI != wantURI || content.MIMEType != literalMIMEFor(path) {
			t.Errorf("%s content = %+v, want uri %q with the path's mime type", path, content, wantURI)
		}
		want, ref := skill.ShowFile(context.Background(), &skill.Request{}, path)
		if ref != nil {
			t.Fatalf("skill show %s: %v", path, ref)
		}
		if !bytes.Equal([]byte(content.Text), want) {
			t.Errorf("resources/read %s differs from skill show bytes", path)
		}
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
		got := frames[strconv.Itoa(i+1)].Error
		if got == nil || got.Code != codeResourceNotFound {
			t.Fatalf("resources/read %q = %+v, want -32002", uri, got)
		}
		if !strings.Contains(got.Message, uri) {
			t.Errorf("resources/read %q message = %q, want the URI", uri, got.Message)
		}
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
