// Package repro stores draft reproduction evidence inside a review bundle.
package repro

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
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
	MaxBytes           = 64 << 10
	maxCommand         = 4096
	hashPrefixLength   = 12
	artifactMatchParts = 4
	directory          = "tests"
	directoryMode      = 0o700
	fileMode           = 0o600
)

var (
	identity     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	artifactName = regexp.MustCompile(`^([a-f0-9]{40})-([a-f0-9]{12})(\.[a-zA-Z0-9]{1,10})$`)
	extension    = regexp.MustCompile(`^\.[a-zA-Z0-9]{1,10}$`)
)

// Receipt points to retained evidence; Command runs from the bundle directory.
type Receipt struct {
	Path     string `json:"path"`
	Command  string `json:"command"`
	Redacted bool   `json:"redacted"`
}

type fileWriter func(*os.Root, string, []byte) error

// Capture validates agent input, then atomically retains its redacted bytes.
// Dry runs perform all reads and validation but never create files.
func Capture(dir, sig, source, command string, dry bool) (*Receipt, error) {
	return capture(dir, sig, source, command, dry, writeImmutable)
}

func capture(dir, sig, source, command string, dry bool, write fileWriter) (*Receipt, error) {
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
	scrubbed := envsecret.ScrubShapes(string(content))
	name := filepath.Join(directory, sig+"-"+contentHash([]byte(scrubbed))+ext)
	if source == "" {
		command = "sh {artifact}"
	}
	cleanCommand := envsecret.ScrubShapes(command)
	receipt := &Receipt{
		Path: filepath.ToSlash(name), Command: strings.ReplaceAll(cleanCommand, "{artifact}", filepath.ToSlash(name)),
		Redacted: containsRedaction(scrubbed, cleanCommand),
	}
	if err := validateDestinations(root, name, []byte(scrubbed), receipt); err != nil {
		return nil, err
	}
	if dry {
		return receipt, nil
	}
	if err := persist(root, name, scrubbed, receipt, write); err != nil {
		return nil, err
	}
	return receipt, nil
}

func captureContent(root *os.Root, source, command string) (string, []byte, error) {
	if strings.Contains(source, `\`) {
		return "", nil, invalid("agent reproduction paths cannot contain backslashes")
	}
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

func persist(root *os.Root, name, scrubbed string, receipt *Receipt, write fileWriter) error {
	if err := root.Mkdir(directory, directoryMode); err != nil && !os.IsExist(err) {
		return jq.Sealed("create reproduction directory", err)
	}
	if err := noLinks(root, name, true); err != nil {
		return err
	}
	if err := write(root, name, []byte(scrubbed)); err != nil {
		return jq.Sealed("retain reproduction", err)
	}
	metadata := sidecarName(name)
	if err := noLinks(root, metadata, true); err != nil {
		return err
	}
	raw, err := jq.Marshal(receipt)
	if err != nil {
		return jq.Sealed("encode reproduction command", err)
	}
	if err := write(root, metadata, raw); err != nil {
		return jq.Sealed("retain reproduction command", err)
	}
	return nil
}

// Read serves only a retained artifact belonging to this finding.
func Read(dir, sig, path string) ([]byte, error) {
	parts := artifactName.FindStringSubmatch(filepath.Base(path))
	if len(parts) != artifactMatchParts || parts[1] != sig || filepath.ToSlash(filepath.Join(directory, filepath.Base(path))) != path {
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
	if contentHash(raw) != parts[2] {
		return nil, invalid("reproduction content hash mismatch")
	}
	return []byte(envsecret.ScrubShapes(string(raw))), nil
}

func read(root *os.Root, name string) ([]byte, error) {
	wire, err := internalName(name)
	if err != nil {
		return nil, err
	}
	name = filepath.FromSlash(wire)
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
	return openRead(root, name, before)
}

func openRead(root *os.Root, name string, before os.FileInfo) ([]byte, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|nonblockRead, 0)
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
	wire, err := internalName(name)
	if err != nil {
		return err
	}
	parts := strings.Split(wire, "/")
	for i := range parts {
		if err := inspectComponent(root, filepath.Join(parts[:i+1]...), allowMissing); err != nil {
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

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:hashPrefixLength]
}

func writeImmutable(root *os.Root, name string, data []byte) error {
	_, err := root.Lstat(name)
	if err == nil {
		return compareExisting(root, name, data)
	}
	if !os.IsNotExist(err) {
		return jq.Sealed("inspect retained reproduction", err)
	}
	created, err := atomic.CreateFileModeIn(root, name, data, fileMode)
	if err != nil {
		return jq.Sealed("create reproduction", err)
	}
	if created {
		return nil
	}
	return compareExisting(root, name, data)
}

func compareExisting(root *os.Root, name string, data []byte) error {
	prior, err := read(root, name)
	if err != nil {
		return err
	}
	if !bytes.Equal(prior, data) {
		return invalid("reproduction name already contains different bytes")
	}
	return nil
}

func internalName(name string) (string, error) {
	wire := strings.ReplaceAll(name, `\`, "/")
	if !fs.ValidPath(wire) || wire == "." {
		return "", invalid("reproduction path must stay inside the bundle")
	}
	return wire, nil
}

func sidecarName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".command.json"
}

func validateDestinations(root *os.Root, name string, content []byte, receipt *Receipt) error {
	raw, err := jq.Marshal(receipt)
	if err != nil {
		return jq.Sealed("encode reproduction command", err)
	}
	if err := validateDestination(root, name, content); err != nil {
		return err
	}
	return validateDestination(root, sidecarName(name), raw)
}

func validateDestination(root *os.Root, name string, data []byte) error {
	if err := noLinks(root, name, true); err != nil {
		return err
	}
	_, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return jq.Sealed("inspect reproduction destination", err)
	}
	return compareExisting(root, name, data)
}
