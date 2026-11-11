// Package curlauth hands a host credential to curl without putting it on the
// command line: the client reads a one-directive config document from stdin, or
// from a 0600 file when the call already uses stdin for a request body. Every
// quote and escape lives here, so no caller assembles an auth directive of its
// own.
package curlauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrLineBreak refuses a credential carrying CR or LF: a curl config document
// is line-oriented, so a break could end this directive and start another.
var ErrLineBreak = errors.New("value carries a line break")

// Credential is one curl config directive carrying a header or a user pair.
type Credential struct {
	line string
}

// credFileMode is the one permission a credential file is allowed: the owner
// reads it, nobody else does.
const credFileMode = 0o600

// Header returns a credential that sends one `name: value` request header.
func Header(name, value string) (Credential, error) {
	quoted, ok := quote(name + ": " + value)
	if !ok {
		return Credential{}, failure{step: "header " + name, err: ErrLineBreak}
	}
	return Credential{line: "header = " + quoted}, nil
}

// Basic returns a credential that sends user and password as HTTP basic auth.
func Basic(user, password string) (Credential, error) {
	quoted, ok := quote(user + ":" + password)
	if !ok {
		return Credential{}, failure{step: "basic auth", err: ErrLineBreak}
	}
	return Credential{line: "user = " + quoted}, nil
}

// StdinArgv returns curl's argv for a call that feeds the credential on stdin,
// plus the exact config document to write there.
func (c Credential) StdinArgv(flags ...string) (argv []string, stdin string) {
	argv = append([]string{"--config", "-"}, flags...)
	return argv, c.Stdin()
}

// Stdin is the config document curl parses.
func (c Credential) Stdin() string {
	if c.line == "" {
		return ""
	}
	return c.line + "\n"
}

// FileArgv returns curl's argv for a call whose stdin carries a request body,
// plus a cleanup to run once the call finishes. The credential lands in a fresh
// 0600 file under a private temp directory that never sits in a repository or
// store.
func (c Credential) FileArgv(flags ...string) (argv []string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "verdict-curl-")
	if err != nil {
		return nil, nil, failure{step: "curl credential dir", err: err}
	}
	cleanup = func() {
		_ = os.RemoveAll(dir)
	}
	path := filepath.Join(dir, "auth.conf")
	if werr := os.WriteFile(path, []byte(c.Stdin()), credFileMode); werr != nil {
		cleanup()
		return nil, nil, failure{step: "curl credential file", err: werr}
	}
	return append([]string{"--config", path}, flags...), cleanup, nil
}

// quote wraps one config value in double quotes, escaping the two bytes curl's
// config parser reads as escapes. ok=false refuses a CR or LF.
func quote(s string) (quoted string, ok bool) {
	if strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(s) {
		if s[i] == '\\' || s[i] == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String(), true
}

// failure names the step that could not build a credential document.
type failure struct {
	err  error
	step string
}

func (f failure) Error() string { return f.step + ": " + f.err.Error() }

func (f failure) Unwrap() error { return f.err }
