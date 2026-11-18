// Package quality scores recorded reviews against hand-known defects in the
// backtest fixtures corpus. It is pure and offline: no git, no agents, no host
// calls. The CLI surface is `verdict backtest fixtures`.
package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	catalogName     = "fixtures.json"
	metaName        = "meta.json"
	reviewName      = "review.json"
	findingsName    = "findings.json"
	schemaVersion   = "1"
	maxCatalogBytes = 1 << 20
	maxReviewBytes  = 1 << 20
)

// Catalog is the fixtures.json manifest: the two reviewed ranges, their
// known and bonus defects, the hand scores, and the praise/defect-verb
// patterns the calibration table prints.
type Catalog struct {
	SchemaVersion string           `json:"schema_version"`
	Fixtures      []Fixture        `json:"fixtures"`
	Calibration   []CalibrationRow `json:"calibration"`
	ReviewSignals ReviewSignals    `json:"review_signals"`
}

// Fixture is one reviewed range with hand-known defects.
type Fixture struct {
	ID      string        `json:"id"`
	Label   string        `json:"label"`
	Repo    string        `json:"repo"`
	Base    string        `json:"base"`
	Head    string        `json:"head"`
	Context []ContextFile `json:"context"`
	Known   []Defect      `json:"known"`
	Bonus   []Defect      `json:"bonus"`
}

// ContextFile is a spec or research file the reviewer was handed, with the
// sha256 of the shipped bytes pinned in the catalog.
type ContextFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// Defect is one known defect. Matchers is a list of rules: all patterns in a
// rule must match, and any rule may match.
type Defect struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Matchers    [][]string `json:"matchers"`
}

// CalibrationRow is one hand score from the archived runs.
type CalibrationRow struct {
	Recording string   `json:"recording"`
	Fixture   string   `json:"fixture"`
	Driver    string   `json:"driver"`
	Hand      []string `json:"hand"`
	HandLabel string   `json:"hand_label"`
}

// ReviewSignals carries the two regexes the calibration table uses to count
// praise-only comments and defect-verb claims.
type ReviewSignals struct {
	PraiseRE     string `json:"praise_re"`
	DefectVerbRE string `json:"defect_verb_re"`
}

// RecordingMeta is the outcome captured beside an archived or live review.
// A nil GatePassed means there was no gate result, never a pass.
type RecordingMeta struct {
	SchemaVersion string `json:"schema_version"`
	Fixture       string `json:"fixture"`
	Driver        string `json:"driver"`
	Source        string `json:"source"`
	GateSource    string `json:"gate_source"`
	GatePassed    *bool  `json:"gate_passed"`
	InfraError    string `json:"infra_error"`
	RefusalCode   string `json:"refusal_code"`
	Note          string `json:"note"`
}

// Finding is the canonical input to a matcher. Legacy calibration reviews
// fill Claim from body/text; S1-format findings fill it from claim/body.
type Finding struct {
	Path     string
	Claim    string
	Severity string
	Quote    string
	Refusal  string
	Anchored *bool
}

// LoadCatalog reads and validates the shipped catalog at path.
func LoadCatalog(path string) (*Catalog, *core.RefusalError) {
	raw, err := jq.CappedFile(path, maxCatalogBytes)
	if err != nil {
		return nil, invalidCatalog(path, "unreadable or larger than 1 MiB")
	}
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, invalidCatalog(path, "not a fixtures catalog: "+err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidCatalog(path, "trailing data after the catalog object")
	}
	if ref := validateCatalog(&c, path); ref != nil {
		return nil, ref
	}
	return &c, nil
}

func invalidCatalog(path, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "fixtures-invalid", Message: "fixtures catalog " + path + " is " + reason}
}

func validateCatalog(c *Catalog, path string) *core.RefusalError {
	if c.SchemaVersion != schemaVersion {
		return invalidCatalog(path, `missing schema_version "1"`)
	}
	if len(c.Fixtures) == 0 {
		return invalidCatalog(path, "missing fixtures")
	}
	if len(c.Calibration) == 0 {
		return invalidCatalog(path, "missing calibration rows")
	}
	seen := map[string]bool{}
	for i := range c.Fixtures {
		if ref := validateFixture(&c.Fixtures[i], seen, path); ref != nil {
			return ref
		}
	}
	recording := map[string]bool{}
	for _, row := range c.Calibration {
		if row.Recording == "" || recording[row.Recording] {
			return invalidCatalog(path, "calibration repeats recording "+row.Recording)
		}
		recording[row.Recording] = true
		if !seen[row.Fixture] {
			return invalidCatalog(path, "calibration names unknown fixture "+row.Fixture)
		}
	}
	if !validRegex(c.ReviewSignals.PraiseRE) || !validRegex(c.ReviewSignals.DefectVerbRE) {
		return invalidCatalog(path, "review_signals carries an invalid pattern")
	}
	return nil
}

func validateFixture(f *Fixture, seen map[string]bool, path string) *core.RefusalError {
	if f.ID == "" || seen[f.ID] {
		return invalidCatalog(path, "missing or repeated fixture id "+f.ID)
	}
	seen[f.ID] = true
	if f.Repo == "" || !commitID(f.Base) || !commitID(f.Head) {
		return invalidCatalog(path, "fixture "+f.ID+" needs repo and 40-hex base and head")
	}
	for _, c := range f.Context {
		if filepath.IsAbs(c.Path) || !digestID(c.SHA256) || c.Bytes <= 0 {
			return invalidCatalog(path, "fixture "+f.ID+" has an invalid context pin")
		}
	}
	if ref := validateDefects(f.Known, f.ID, path); ref != nil {
		return ref
	}
	return validateDefects(f.Bonus, f.ID, path)
}

func validateDefects(defects []Defect, id, path string) *core.RefusalError {
	if len(defects) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, d := range defects {
		if d.ID == "" || d.Description == "" || seen[d.ID] {
			return invalidCatalog(path, "fixture "+id+" has a missing or repeated defect id")
		}
		seen[d.ID] = true
		if len(d.Matchers) == 0 {
			return invalidCatalog(path, "defect "+d.ID+" has no matchers")
		}
		for _, rule := range d.Matchers {
			if len(rule) == 0 {
				return invalidCatalog(path, "defect "+d.ID+" has an empty rule")
			}
			for _, pattern := range rule {
				if !validRegex(pattern) {
					return invalidCatalog(path, "defect "+d.ID+" has invalid pattern "+pattern)
				}
			}
		}
	}
	return nil
}

func validRegex(pattern string) bool {
	_, err := regexp.Compile("(?i)" + pattern)
	return err == nil
}

func digestID(s string) bool { return hexID(s, 64) }

func commitID(s string) bool { return hexID(s, 40) }

func hexID(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// FixtureByID finds one fixture, or refuses with a literal code.
func FixtureByID(c *Catalog, id string) (*Fixture, *core.RefusalError) {
	if id == "" {
		return nil, nil
	}
	for i := range c.Fixtures {
		if c.Fixtures[i].ID == id {
			return &c.Fixtures[i], nil
		}
	}
	return nil, &core.RefusalError{Code: "fixtures-invalid", Message: "unknown fixture '" + id + "'"}
}

// LoadRecordingMeta reads one recording's outcome beside its review.
func LoadRecordingMeta(dir string) (*RecordingMeta, *core.RefusalError) {
	raw, err := jq.CappedFile(filepath.Join(dir, metaName), maxReviewBytes)
	if err != nil {
		return nil, invalidRecording(dir, "unreadable meta.json")
	}
	var m RecordingMeta
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, invalidRecording(dir, "unreadable meta.json: "+err.Error())
	}
	if m.SchemaVersion != schemaVersion || m.Fixture == "" {
		return nil, invalidRecording(dir, "missing schema_version or fixture")
	}
	if m.Source != reviewName && m.Source != findingsName {
		return nil, invalidRecording(dir, "source must be review.json or findings.json")
	}
	return &m, nil
}

func invalidRecording(dir, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "fixtures-recording-invalid", Message: "recording " + dir + " is " + reason}
}

// RecordingDirs lists one recording per directory under root. A path is one
// directory element, so an operator-supplied recordings root cannot escape by
// naming another tree through a recording id.
func RecordingDirs(root string) ([]string, *core.RefusalError) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, &core.RefusalError{Code: "fixtures-recording-invalid", Message: "cannot read recordings root " + root}
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() != filepath.Base(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(root, entry.Name()))
	}
	return dirs, nil
}
