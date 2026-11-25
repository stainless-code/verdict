package reportcore

import "github.com/stainless-code/verdict/internal/core"

// Flags carries the resolved report options: the adapter parses argv
// and the service receives values only. The gating fields (Check, Live,
// MaxRisk, Completeness, Coverage) are composed by the gate verb alone, so
// they stay out of the MCP tool schema (`json:"-"`): `report` is the advisory
// read and cannot fail an exit.
type Flags struct {
	// Coverage is the diff-coverage posture: `require` fails the gate on an
	// absent or partial stored attestation, `warn` reports the same finding
	// and never fails. Empty means the run does not compose the dimension at
	// all, which is what keeps `report` advisory.
	Coverage string `enum:"require|warn"                                                            json:"-"`
	Status   string `enum:"all|open|addressed|wont-fix|false-positive|deferred"`
	Kind     string `enum:"all|inline|section"`
	Category string `enum:"logic|edge-case|security|performance|accidental-commit|style|docs|other"`
	Output   string
	Sarif    string
	Check    string `enum:"critical|important|informational" json:"-"`
	// Compare names a baseline bundle to compare the reported one against: a
	// bundle directory, or a PR/local slug resolved in the same repository
	// state directory. CompareSet records that a caller named the flag at all,
	// so an explicit empty value refuses instead of silently disabling the
	// comparison; it is transport bookkeeping and stays out of the tool schema.
	Compare string
	Live    string `enum:"auto|strict|skip"         json:"-"`
	MaxRisk string `enum:"low|medium|high|critical" json:"-"`
	// Policy names a candidate policy file to replay the store under: the
	// report derives its gate and approval twice, once under the captured
	// policy and once under this one, and writes nothing. It stays out of the
	// MCP tool schema (`json:"-"`) because the replay is a CLI question about
	// a file on disk, not a stored artifact the tool can read.
	Policy          string `json:"-"`
	IncludeWithheld bool
	Cost            bool
	Completeness    bool `json:"-"`
	Provenance      bool
	Risk            bool
	CompareSet      bool `json:"-"`
	// Validation projects the trusted pass: the receipt a post or record run
	// stored, else a tally of validation.json. It stays out of the MCP tool
	// schema (`json:"-"`), like Coverage, because the MCP report tool is the
	// advisory read.
	Validation bool `json:"-"`
}

// Request is the report service invocation: the read runtime and
// positionals, the resolved options, the render configuration, and the
// warnings the adapter already accumulated (prefs).
type Request struct { //nolint:govet // request grouping; the struct is one call, not a hot array
	core.ReadRequest
	Flags     Flags
	Effective core.Effective
	Warnings  []string
	// OverridesDir is the overrides directory this run validates. Empty keeps
	// the CLI's cwd-relative `.verdict/overrides`; a served root passes
	// `<root>/.verdict/overrides` so every repository is judged by its own
	// policy rather than the launch directory's. It is a transport seam, not
	// an MCP argument, so it stays out of the generated tool schema.
	OverridesDir string `json:"-"`
	JSONOnly     bool   `json:"-"`
}

// Response carries the report's typed data, warnings, and pure render
// inputs. Human is the human-mode summary (one line per element, no
// trailing newline), Next is the envelope's next field, CheckLines are the
// gate diagnostics written after output, and the artifacts are returned for
// the adapter to write.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo       string
	Pr         string
	At         string
	Data       *core.ReportData
	Warnings   []string
	Next       string
	Human      []string
	CheckLines []string
	GateFailed bool
	Output     *core.FileArtifact
	Sarif      *core.FileArtifact
}
