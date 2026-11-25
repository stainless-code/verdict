package gitsource

import (
	"testing"
)

// allowedSources is every shape the shared allowlist accepts, with the
// canonical URL and ref it must produce.
var allowedSources = []struct {
	name   string
	source string
	url    string
	ref    string
}{
	{"https", "https://host/x.git", "https://host/x.git", ""},
	{"https port", "https://host:8443/x.git", "https://host:8443/x.git", ""},
	{"https ipv4", "https://127.0.0.1:8443/x.git", "https://127.0.0.1:8443/x.git", ""},
	{"https ipv6", "https://[2001:db8::1]:8443/x.git", "https://[2001:db8::1]:8443/x.git", ""},
	{"https ipv6 no port", "https://[2001:db8::1]/x.git", "https://[2001:db8::1]/x.git", ""},
	{"ssh", "ssh://git@host/x.git", "ssh://git@host/x.git", ""},
	{"ssh port", "ssh://deploy-bot@host:22/x", "ssh://deploy-bot@host:22/x", ""},
	{"ssh ipv6", "ssh://git@[2001:db8::1]:22/x", "ssh://git@[2001:db8::1]:22/x", ""},
	{"scp", "git@host:acme/x.git", "git@host:acme/x.git", ""},
	{"scp ipv6", "git@[2001:db8::1]:acme/x.git", "git@[2001:db8::1]:acme/x.git", ""},
	{"uppercase scheme", "HTTPS://host/x.git", "https://host/x.git", ""},
	{"https ref", "https://host/x.git@v1.2.3", "https://host/x.git", "v1.2.3"},
	{"scp ref", "git@host:acme/x.git@release/1", "git@host:acme/x.git", "release/1"},
	{"ssh ref", "ssh://deploy-bot@host:22/x@v1", "ssh://deploy-bot@host:22/x", "v1"},
}

// refusedSources is every malformed or credential-bearing shape the allowlist
// must reject outright.
var refusedSources = []struct {
	name   string
	source string
}{
	{"bracketed host garbage", "https://[not a host]:443/packs.git"},
	{"bracketed host empty", "https://[]/packs.git"},
	{"bracketed ssh garbage", "ssh://git@[garbage]/packs.git"},
	{"bracketed ssh empty", "ssh://git@[]/packs.git"},
	{"bracketed scp garbage", "git@[not a host]:acme/x.git"},
	{"bracketed no colon", "https://[2001db8]/x.git"},
	{"bracketed unterminated", "https://[2001:db8::1/x.git"},
	{"bracketed port garbage", "https://[2001:db8::1]:port/x.git"},
	{"bracketed port empty", "https://[2001:db8::1]:/x.git"},
	{"bracketed host trailing name", "https://[2001:db8::1]x/x.git"},
	{"bracketed triple colon", "https://[2001:::1]/packs.git"},
	{"bracketed zoned", "https://[fe80::1%25eth0]/packs.git"},
	{"bracketed ipv4 only", "https://[1.2.3.4]/packs.git"},
	{"http scheme", "http://example.invalid/packs.git"},
	{"file scheme", "file:///etc/packs"},
	{"git scheme", "git://host/x.git"},
	{"one slash", "https:/user:S3CRET@host/acme/packs.git"},
	{"https password", "https://oauth2:ghp_SECRET@host/packs.git"},
	{"ssh password", "ssh://user:S3CRET@host/x"},
	{"scp password", "oauth2:S3CRET@host:acme/packs.git"},
	{"ssh without user", "ssh://host/x.git"},
	{"no path", "https://host"},
	{"leading dash path", "https://host/-x"},
	{"space in path", "https://host/x y"},
	{"query", "https://host/x.git?private_token=TOK"},
	{"fragment", "https://host/x.git#TOK"},
	{"invalid ref", "https://host/x.git@-bad"},
	{"path scheme", "git@host://acme/packs.git"},
	{"port zero", "https://host:0/x.git"},
	{"port overflow", "https://host:65536/x.git"},
	{"port large", "https://host:99999/x.git"},
	{"ipv6 port overflow", "https://[2001:db8::1]:65536/x.git"},
	{"ipv6 port zero", "https://[2001:db8::1]:0/x.git"},
}

// TestParseAllows proves every accepted shape parses to its canonical URL and
// ref.
func TestParseAllows(t *testing.T) {
	t.Parallel()
	for _, tc := range allowedSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Parse(tc.source)
			if !ok || got.URL != tc.url || got.Ref != tc.ref {
				t.Fatalf("Parse(%q) = %+v, %v; want url %q ref %q", tc.source, got, ok, tc.url, tc.ref)
			}
		})
	}
}

// TestParseRefuses proves every malformed or credential-bearing shape does
// not parse.
func TestParseRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range refusedSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := Parse(tc.source); ok {
				t.Fatalf("Parse(%q) = %+v, want refused", tc.source, got)
			}
		})
	}
}

// TestParseURLRefusesRefSuffix proves the URL-only entry point never reads an
// @suffix as a ref: a caller that stores the ref in its own field would
// otherwise accept a value whose tail is silently dropped.
func TestParseURLRefusesRefSuffix(t *testing.T) {
	t.Parallel()
	for _, tc := range []string{"https://host/x.git@v1.2.3", "ssh://git@host/x@v1", "git@host:acme/x.git@release/1"} {
		if got, ok := ParseURL(tc); ok {
			t.Errorf("ParseURL(%q) = %q, want refused", tc, got)
		}
	}
	if got, ok := ParseURL("HTTPS://host/x.git"); !ok || got != "https://host/x.git" {
		t.Fatalf("ParseURL uppercase scheme = %q, %v; want the lower-case URL", got, ok)
	}
}

// TestLooksLike proves a remote-shaped value is never read as a directory and a
// local path never asks to be a clone.
func TestLooksLike(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"https://host/x.git", "https:/host/x.git", "git@host:acme/x.git"} {
		if !LooksLike(want) {
			t.Errorf("LooksLike(%q) = false, want true", want)
		}
	}
	for _, want := range []string{"dir:src", "src-packs", "C:/packs", "./packs/org"} {
		if LooksLike(want) {
			t.Errorf("LooksLike(%q) = true, want a path", want)
		}
	}
}

// TestSecretShaped proves every URL or userinfo shape is flagged for masking,
// and a bare path or repository spelling is not.
func TestSecretShaped(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"https://oauth2:ghp_SECRET@host/x", "https:/ghp_TOKEN@host/x", "user:TOK@host:x",
	} {
		if !SecretShaped(want) {
			t.Errorf("SecretShaped(%q) = false, want true", want)
		}
	}
	for _, want := range []string{"central", "packs/org", "builtin"} {
		if SecretShaped(want) {
			t.Errorf("SecretShaped(%q) = true, want false", want)
		}
	}
}

// TestRefGrammarGatesRefSuffix proves the @suffix grammar is the ref shape a
// git argv can take: a leading dash never reaches one.
func TestRefGrammarGatesRefSuffix(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"v1.2.3", "release/1", "abc123", "A.b_c-d"} {
		if !refGrammar.MatchString(ref) {
			t.Errorf("refGrammar refused %q", ref)
		}
	}
	for _, ref := range []string{"-bad", "with space", "semi;colon"} {
		if refGrammar.MatchString(ref) {
			t.Errorf("refGrammar accepted %q", ref)
		}
	}
}
