package curlauth_test

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/curlauth"
)

// TestHeaderConfigLine pins the one-line header document.
func TestHeaderConfigLine(t *testing.T) {
	t.Parallel()
	cred, err := curlauth.Header("Authorization", "token eval-token")
	if err != nil {
		t.Fatal(err)
	}
	if want := "header = \"Authorization: token eval-token\"\n"; cred.Stdin() != want {
		t.Errorf("config = %q, want %q", cred.Stdin(), want)
	}
}

// TestHeaderQuotesQuoteAndBackslash proves curl's config quoting keeps a token
// carrying a quote and a backslash from breaking out of the directive.
func TestHeaderQuotesQuoteAndBackslash(t *testing.T) {
	t.Parallel()
	cred, err := curlauth.Header("Authorization", `token a"b\c`)
	if err != nil {
		t.Fatal(err)
	}
	want := "header = \"Authorization: token a\\\"b\\\\c\"\n"
	if cred.Stdin() != want {
		t.Errorf("config = %q, want %q", cred.Stdin(), want)
	}
}

// TestBasicConfigLine pins the quoted user directive.
func TestBasicConfigLine(t *testing.T) {
	t.Parallel()
	cred, err := curlauth.Basic("user", "p@ss:word")
	if err != nil {
		t.Fatal(err)
	}
	if want := "user = \"user:p@ss:word\"\n"; cred.Stdin() != want {
		t.Errorf("config = %q, want %q", cred.Stdin(), want)
	}
}

// TestLineBreakRefused proves a credential carrying CR or LF refuses before a
// config document exists.
func TestLineBreakRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cred func() (curlauth.Credential, error)
		name string
	}{
		{name: "header", cred: func() (curlauth.Credential, error) {
			return curlauth.Header("Authorization", "token a\nb")
		}},
		{name: "basic user", cred: func() (curlauth.Credential, error) {
			return curlauth.Basic("u\nser", "pass")
		}},
		{name: "basic password", cred: func() (curlauth.Credential, error) {
			return curlauth.Basic("user", "pa\rss")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.cred(); !errors.Is(err, curlauth.ErrLineBreak) {
				t.Fatalf("err = %v, want ErrLineBreak", err)
			}
		})
	}
}

// TestStdinArgv pins the read shape: config first, then the caller's flags.
func TestStdinArgv(t *testing.T) {
	t.Parallel()
	cred, err := curlauth.Header("Authorization", "token eval-token")
	if err != nil {
		t.Fatal(err)
	}
	argv, stdin := cred.StdinArgv("-sf", "https://forgejo.invalid/api/v1")
	wantArgv := []string{"--config", "-", "-sf", "https://forgejo.invalid/api/v1"}
	if strings.Join(argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q, want %q", argv, wantArgv)
	}
	if want := "header = \"Authorization: token eval-token\"\n"; stdin != want {
		t.Errorf("stdin = %q, want %q", stdin, want)
	}
}

// TestFileArgvOwnerOnlyAndCleaned pins the write shape: a 0600 file whose
// cleanup removes it.
func TestFileArgvOwnerOnlyAndCleaned(t *testing.T) {
	t.Parallel()
	cred, err := curlauth.Header("Authorization", "token eval-token")
	if err != nil {
		t.Fatal(err)
	}
	argv, cleanup, err := cred.FileArgv("-sf", "https://forgejo.invalid/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	path := configPath(t, argv)
	assertConfigFile(t, path, "header = \"Authorization: token eval-token\"\n")
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("credential file survived cleanup: %v", err)
	}
}

// assertConfigFile pins one credential file's mode and exact content.
func assertConfigFile(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("credential file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Errorf("file = %q, want %q", string(raw), want)
	}
}

func configPath(t *testing.T, argv []string) string {
	t.Helper()
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--config" {
			if argv[i+1] == "-" {
				t.Fatalf("argv = %q, want a file config", argv)
			}
			return argv[i+1]
		}
	}
	t.Fatalf("argv = %q, want --config", argv)
	return ""
}
