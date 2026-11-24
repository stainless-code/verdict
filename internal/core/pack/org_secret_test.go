package pack

import (
	"context"
	"strings"
	"testing"
)

func containsSecret(msg, orig string) bool {
	// Credential-bearing URL original contains a secret token; check message does not contain the token part.
	if strings.Contains(orig, "ghp_") || strings.Contains(orig, "SECRET") {
		return strings.Contains(msg, "ghp_") || strings.Contains(msg, "SECRET")
	}
	// http/file URLs should not echo the full URL either if it looks secret, but generic check: if orig has userinfo, ensure not in msg verbatim
	at := strings.Index(orig, "@")
	if at > 0 && strings.Contains(orig[:at], ":") {
		// user:pass@...
		parts := strings.SplitN(orig, "@", 2)
		return strings.Contains(msg, parts[0])
	}
	return false
}

func TestSyncRejectsCredentialURL(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []string{
		"https://oauth2:ghp_SECRET123@host/packs.git",
		"http://example.com/packs.git",
		"file:///etc/packs",
	}
	for _, tc := range cases {
		pin := &OrgPin{Repo: tc, Ref: sha, Path: "."}
		_, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin})
		if ref == nil {
			t.Fatalf("repo %s: expected refusal, got nil", tc)
		}
		if ref.Code != CodeBadPackSource {
			t.Fatalf("repo %s: code = %s, want %s", tc, ref.Code, CodeBadPackSource)
		}
		// Refusal must not echo the secret.
		if containsSecret(ref.Message, tc) {
			t.Fatalf("repo %s: refusal echoed secret: %s", tc, ref.Message)
		}
	}
	_ = repo
}
