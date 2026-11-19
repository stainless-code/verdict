// Package quality scores recorded reviews against hand-known defects in the
// backtest fixtures corpus. It is pure and offline: no git, no agents, no host
// calls. The CLI surface is `verdict backtest fixtures`.
package quality

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	catalogName     = "fixtures.json"
	schemaVersion   = "1"
	maxCatalogBytes = 1 << 20
	maxReviewBytes  = 1 << 20
	fixturePrefix   = "fixture "
	defectPrefix    = "defect "
	sha256HexLen    = 64
	commitHexLen    = 40
	dirPermissions  = 0o755
	isText          = " is "
)

// Catalog is the fixtures.json manifest: the two reviewed ranges, their
// known and bonus defects, the hand scores, and the praise/defect-verb
// patterns the calibration table prints.
type Catalog struct {
	ReviewSignals ReviewSignals    `json:"review_signals"`
	SchemaVersion string           `json:"schema_version"`
	Fixtures      []Fixture        `json:"fixtures"`
	Calibration   []CalibrationRow `json:"calibration"`
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
	Diff    ContextFile   `json:"diff"`
}

// ContextFile is one file the corpus ships beside a fixture, with the sha256
// of its bytes pinned in the catalog: a spec or research document the reviewer
// was handed, or the fixture's reviewed full.diff.
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
	HandLabel string   `json:"hand_label"`
	Hand      []string `json:"hand"`
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
	SourceBundle  string `json:"source_bundle"`
	SourceSHA256  string `json:"source_sha256"`
	InfraError    string `json:"infra_error"`
	RefusalCode   string `json:"refusal_code"`
	Note          string `json:"note"`
}

// Finding is the canonical input to a matcher. Claim comes from the fixture
// recording's text field; Line, StartLine, Side and Quote feed the quote
// verifier.
type Finding struct {
	Anchored  *bool
	Path      string
	Claim     string
	Severity  string
	Quote     string
	Refusal   string
	Side      string
	Line      int
	StartLine int
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
	return &core.RefusalError{Code: "fixtures-invalid", Message: "fixtures catalog " + path + isText + reason}
}

func validateCatalog(c *Catalog, path string) *core.RefusalError {
	if c.SchemaVersion != schemaVersion || len(c.Fixtures) == 0 || len(c.Calibration) == 0 {
		return invalidCatalog(path, "missing schema_version, fixtures, or calibration rows")
	}
	seen := map[string]bool{}
	for i := range c.Fixtures {
		if ref := validateFixture(&c.Fixtures[i], seen, path); ref != nil {
			return ref
		}
	}
	if ref := validateCalibration(c.Calibration, seen, path); ref != nil {
		return ref
	}
	return validateSignals(c.ReviewSignals, path)
}

func validateSignals(s ReviewSignals, path string) *core.RefusalError {
	if !validRegex(s.PraiseRE) || !validRegex(s.DefectVerbRE) {
		return invalidCatalog(path, "review_signals carries an invalid pattern")
	}
	return nil
}

func validateCalibration(rows []CalibrationRow, fixtures map[string]bool, path string) *core.RefusalError {
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Recording == "" || seen[row.Recording] {
			return invalidCatalog(path, "calibration repeats recording "+row.Recording)
		}
		seen[row.Recording] = true
		if !fixtures[row.Fixture] {
			return invalidCatalog(path, "calibration names unknown fixture "+row.Fixture)
		}
	}
	return nil
}

func validateFixture(f *Fixture, seen map[string]bool, path string) *core.RefusalError {
	if f.ID == "" || seen[f.ID] {
		return invalidCatalog(path, "missing or repeated fixture id "+f.ID)
	}
	seen[f.ID] = true
	if f.Repo == "" || !commitID(f.Base) || !commitID(f.Head) {
		return invalidCatalog(path, fixturePrefix+f.ID+" needs repo and 40-hex base and head")
	}
	if ref := validatePins(f, path); ref != nil {
		return ref
	}
	if len(f.Known) == 0 {
		return invalidCatalog(path, fixturePrefix+f.ID+" has no known defects")
	}
	defectIDs := map[string]bool{}
	if ref := validateDefects(f.Known, f.ID, path, defectIDs); ref != nil {
		return ref
	}
	return validateDefects(f.Bonus, f.ID, path, defectIDs)
}

// validatePins checks the shape of every sha256 pin a fixture carries: the
// context documents it was handed and the reviewed full.diff beside them.
func validatePins(f *Fixture, path string) *core.RefusalError {
	for _, pin := range f.Context {
		if !validPin(pin) {
			return invalidCatalog(path, fixturePrefix+f.ID+" has an invalid context pin")
		}
	}
	if !validPin(f.Diff) {
		return invalidCatalog(path, fixturePrefix+f.ID+" has an invalid diff pin")
	}
	return nil
}

func validPin(pin ContextFile) bool {
	return relativePath(pin.Path) && digestID(pin.SHA256) && pin.Bytes > 0
}

func relativePath(name string) bool {
	if name == "" || strings.ContainsRune(name, '\\') || windowsDrive(name) {
		return false
	}
	local := filepath.FromSlash(name)
	return filepath.IsLocal(local) && filepath.Clean(local) == local
}

// windowsDrive catches `C:...` on POSIX hosts, where filepath treats the
// colon as ordinary text and IsLocal would otherwise accept the path.
func windowsDrive(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// loadFixtureHeads verifies every fixture's pinned context files and loads
// its pinned full.diff bytes for the quote verifier. A changed or unreadable
// pin is a catalog error: the verifier must never inspect unpinned bytes.
func loadFixtureHeads(root string, catalog *Catalog) (map[string][]byte, *core.RefusalError) {
	heads := make(map[string][]byte, len(catalog.Fixtures))
	for i := range catalog.Fixtures {
		f := &catalog.Fixtures[i]
		if ref := verifyContextPins(root, f); ref != nil {
			return nil, ref
		}
		diff, ref := loadPinnedFile(root, f.ID, "diff", &f.Diff)
		if ref != nil {
			return nil, ref
		}
		heads[f.ID] = diff
	}
	return heads, nil
}

func verifyContextPins(root string, f *Fixture) *core.RefusalError {
	for j := range f.Context {
		if _, ref := loadPinnedFile(root, f.ID, "context", &f.Context[j]); ref != nil {
			return ref
		}
	}
	return nil
}

func loadPinnedFile(root, fixtureID, kind string, pin *ContextFile) ([]byte, *core.RefusalError) {
	full := filepath.Join(root, filepath.FromSlash(pin.Path))
	raw, err := jq.CappedFile(full, maxReviewBytes)
	if err != nil {
		return nil, invalidCatalog(full, "unreadable "+kind+" file for fixture "+fixtureID)
	}
	if len(raw) != pin.Bytes {
		return nil, invalidCatalog(full, kind+" byte count for fixture "+fixtureID+isText+
			strconv.Itoa(len(raw))+", pin says "+strconv.Itoa(pin.Bytes))
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != pin.SHA256 {
		return nil, invalidCatalog(full, kind+" sha256 for fixture "+fixtureID+isText+got+
			", pin says "+pin.SHA256)
	}
	return raw, nil
}

func validateDefects(defects []Defect, id, path string, seen map[string]bool) *core.RefusalError {
	for _, d := range defects {
		if ref := checkDefectID(&d, id, path, seen); ref != nil {
			return ref
		}
		if ref := validateRules(d, path); ref != nil {
			return ref
		}
	}
	return nil
}

func checkDefectID(d *Defect, id, path string, seen map[string]bool) *core.RefusalError {
	if d.ID == "" || d.Description == "" {
		return invalidCatalog(path, fixturePrefix+id+" has a missing defect id or description")
	}
	if seen[d.ID] {
		return invalidCatalog(path, defectPrefix+d.ID+" in fixture "+id+" is repeated across known and bonus")
	}
	seen[d.ID] = true
	return nil
}

func validateRules(d Defect, path string) *core.RefusalError {
	if len(d.Matchers) == 0 {
		return invalidCatalog(path, defectPrefix+d.ID+" has no matchers")
	}
	for i, rule := range d.Matchers {
		if ref := validateRule(&d, rule, i, path); ref != nil {
			return ref
		}
	}
	return nil
}

func validateRule(d *Defect, rule []string, i int, path string) *core.RefusalError {
	label := defectPrefix + d.ID + " rule " + strconv.Itoa(i)
	if len(rule) == 0 {
		return invalidCatalog(path, label+" has an empty rule")
	}
	for j, pattern := range rule {
		if strings.TrimSpace(pattern) == "" {
			return invalidCatalog(path, label+" pattern "+strconv.Itoa(j)+" is blank")
		}
	}
	if !validPatterns(rule) {
		return invalidCatalog(path, label+" has invalid pattern")
	}
	return nil
}

func validPatterns(rule []string) bool {
	for _, pattern := range rule {
		if !validRegex(pattern) {
			return false
		}
	}
	return true
}

func validRegex(pattern string) bool {
	_, err := regexp.Compile("(?i)" + pattern)
	return err == nil
}

func digestID(s string) bool { return hexID(s, sha256HexLen) }

func commitID(s string) bool { return hexID(s, commitHexLen) }

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

// LoadRecordingMeta reads the provenance sidecar beside one fixture recording.
func LoadRecordingMeta(path string) (*RecordingMeta, *core.RefusalError) {
	raw, err := jq.CappedFile(path, maxReviewBytes)
	if err != nil {
		return nil, invalidRecording(path, "unreadable meta sidecar")
	}
	m, ref := decodeRecordingMeta(path, raw)
	if ref != nil {
		return nil, ref
	}
	if m.SchemaVersion != schemaVersion || m.Fixture == "" {
		return nil, invalidRecording(path, "missing schema_version or fixture")
	}
	if filepath.IsAbs(m.SourceBundle) || !digestID(m.SourceSHA256) {
		return nil, invalidRecording(path, "invalid source provenance")
	}
	return m, nil
}

func decodeRecordingMeta(path string, raw []byte) (*RecordingMeta, *core.RefusalError) {
	var m RecordingMeta
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, invalidRecording(path, "unreadable meta sidecar: "+err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		reason := "trailing data after the meta sidecar object"
		if err != nil {
			reason += ": " + err.Error()
		}
		return nil, invalidRecording(path, reason)
	}
	return &m, nil
}

func invalidRecording(dir, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "fixtures-recording-invalid", Message: "recording " + dir + isText + reason}
}
