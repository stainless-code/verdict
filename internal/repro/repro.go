// Package repro stores draft reproduction evidence inside a review bundle.
package repro

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
)

// MaxBytes bounds one reproduction file.
const (
	MaxBytes      = 64 << 10
	maxCommand    = 4096
	directory     = "tests"
	directoryMode = 0o700
	fileMode      = 0o600
)

var (
	identity  = regexp.MustCompile(`^[a-f0-9]{40}$`)
	extension = regexp.MustCompile(`^\.[a-zA-Z0-9]{1,10}$`)
)

// Receipt points to retained evidence; Command runs from the bundle directory.
type Receipt struct {
	Path     string `json:"path"`
	Command  string `json:"command"`
	Redacted bool   `json:"redacted"`
}

// Capture validates agent input, then atomically retains its redacted bytes.
// Dry runs perform all reads and validation but never create files.
func Capture(dir, sig, source, command string, dry bool) (*Receipt, error) {
	if err := validateCapture(sig, command); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, jq.Sealed("open reproduction bundle", err)
	}
	defer func() { _ = root.Close() }()
	ext, content, err := captureContent(root, source, command)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(directory, sig+ext)
	if source == "" {
		command = "sh {artifact}"
	}
	scrubbed := envsecret.ScrubShapes(string(content))
	cleanCommand := envsecret.ScrubShapes(command)
	receipt := &Receipt{
		Path: filepath.ToSlash(name), Command: strings.ReplaceAll(cleanCommand, "{artifact}", filepath.ToSlash(name)),
		Redacted: containsRedaction(scrubbed, cleanCommand),
	}
	if dry {
		return receipt, nil
	}
	if err := persist(root, sig, name, scrubbed, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func captureContent(root *os.Root, source, command string) (string, []byte, error) {
	ext := ".sh"
	content := []byte(command + "\n")
	if source != "" {
		ext = filepath.Ext(source)
		if !extension.MatchString(ext) || !strings.Contains(command, "{artifact}") {
			return "", nil, invalid("reproduction needs a simple extension and {artifact} in command")
		}
		var err error
		content, err = read(root, source)
		if err != nil {
			return "", nil, err
		}
	}
	return ext, content, nil
}

func persist(root *os.Root, sig, name, scrubbed string, receipt *Receipt) error {
	if err := root.Mkdir(directory, directoryMode); err != nil && !os.IsExist(err) {
		return jq.Sealed("create reproduction directory", err)
	}
	if err := noLinks(root, name, true); err != nil {
		return err
	}
	if err := atomic.WriteFileModeIn(root, name, []byte(scrubbed), fileMode); err != nil {
		return jq.Sealed("retain reproduction", err)
	}
	metadata := filepath.Join(directory, sig+".command.json")
	if err := noLinks(root, metadata, true); err != nil {
		return err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return jq.Sealed("encode reproduction command", err)
	}
	if err := atomic.WriteFileModeIn(root, metadata, raw, fileMode); err != nil {
		return jq.Sealed("retain reproduction command", err)
	}
	return nil
}

// Read serves only a retained artifact belonging to this finding.
func Read(dir, sig, path string) ([]byte, error) {
	expected := filepath.ToSlash(filepath.Join(directory, sig+filepath.Ext(path)))
	if !identity.MatchString(sig) || expected != path || !extension.MatchString(filepath.Ext(path)) {
		return nil, invalid("invalid reproduction path")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, jq.Sealed("open reproduction bundle", err)
	}
	defer func() { _ = root.Close() }()
	raw, err := read(root, filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	return []byte(envsecret.ScrubShapes(string(raw))), nil
}

func read(root *os.Root, name string) ([]byte, error) {
	if err := noLinks(root, name, false); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, jq.Sealed("inspect reproduction", err)
	}
	if !before.Mode().IsRegular() {
		return nil, invalid("reproduction must be a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, jq.Sealed("open reproduction", err)
	}
	defer func() { _ = file.Close() }()
	return readFile(file, before)
}

func readFile(file *os.File, before os.FileInfo) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, jq.Sealed("stat reproduction", err)
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, invalid("reproduction must be a regular file of at most 64 KiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return nil, jq.Sealed("read reproduction", err)
	}
	if len(raw) > MaxBytes {
		return nil, invalid("reproduction exceeds 64 KiB")
	}
	return raw, nil
}

func noLinks(root *os.Root, name string, allowMissing bool) error {
	if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
		return invalid("reproduction path must stay inside the bundle")
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i := range parts {
		if err := inspectComponent(root, filepath.Join(parts[:i+1]...), allowMissing && i == len(parts)-1); err != nil {
			return err
		}
	}
	return nil
}

func inspectComponent(root *os.Root, name string, allowMissing bool) error {
	info, err := root.Lstat(name)
	if allowMissing && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return jq.Sealed("inspect reproduction path", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return invalid("reproduction path contains a symlink")
	}
	return nil
}

type invalid string

func (e invalid) Error() string { return string(e) }

func validateCapture(sig, command string) error {
	if !identity.MatchString(sig) || command == "" || len(command) > maxCommand {
		return invalid("invalid reproduction identity or command")
	}
	return nil
}

func containsRedaction(content, command string) bool {
	return strings.Contains(content, envsecret.Redacted) || strings.Contains(command, envsecret.Redacted)
}
