package recordcore

// Finding-field names (jq object keys for findings.json entries).
const (
	fieldAnchored   = "anchored"
	fieldAt         = "at"
	fieldBody       = "body"
	fieldDropReason = "dropReason"
	fieldHeading    = "heading"
	fieldHistory    = "history"
	fieldInBounds   = "inBounds"
	fieldKB         = "kb"
	fieldLastSeen   = "lastSeen"
	fieldLine       = "line"
	fieldPath       = "path"
	fieldRigor      = "rigor"
	fieldSeverity   = "severity"
	fieldSig        = "sig"
	fieldSigVersion = "sig_version"
	fieldStatus     = "status"
	fieldSuggestion = "suggestion"
	fieldNote       = "note"
	fieldActor      = "actor"
	fieldVerb       = "verb"
	fieldTo         = "to"
	fieldRule       = "rule"
	fieldOutcome    = "outcome"
)

// Schema and rendering constants.
const (
	// schemaVersion1 is the review.json schema this port understands.
	schemaVersion1 = "1"
	// sigVersion1 stamps newly computed finding signatures.
	sigVersion1 = "1"
	// newline joins rendered body lines.
	newline = "\n"
	// prSuffix is the bundle PR-record path suffix.
	prSuffix = "/PR.json"
	// Provider codes.
	providerLocal = "local"
	localPrefix   = "local-"
	corpusPrefix  = "corpus-"
	// statusOpen is the open finding status.
	statusOpen = "open"
	// Refusal codes.
	codeRecordFailed      = "record-failed"
	codePostFailed        = "post-failed"
	codeNotLocal          = "not-local"
	codeCanceled          = "canceled"
	codeMissingReviewFile = "missing-review-file"
	codeMissingDiff       = "missing-diff"
	codeBadReviewSchema   = "bad-review-schema"
	codeBadReviewInput    = "bad-review-input"
	codeCorruptStore      = "corrupt-store"
	codeMissingChunkSnap  = "missing-chunk-snapshot"
	codeBadTriageStatus   = "bad-triage-status"
	codeMissingChunkCov   = "missing-chunk-coverage"
	// Sig-resolution outcomes.
	sigNone = "NONE"
	sigMany = "MANY"
	// gitBPrefix is the " b/" separator in `diff --git` lines.
	gitBPrefix = " b/"
	// chunkHunksSuffix is the derived per-chunk snapshot filename.
	chunkHunksSuffix = "/.chunk-hunks.tsv"
	// revalidatorForced is the actor tag for forced revalidation.
	revalidatorForced = "revalidator"
	// fieldEvent and fieldSHA are history-entry keys.
	fieldEvent = "event"
	fieldSHA   = "sha"
	// fieldStartLine is the comment anchor start key.
	fieldStartLine = "start_line"
	// outcomeAddressed is the refreshed-triage status.
	outcomeAddressed = "addressed"
)

// Numeric parsing/rendering constants.
const (
	// shortSHALen is the abbreviated commit width.
	shortSHALen = 7
	// sigPrefixLen is the short signature display width (${sig:0:8}).
	sigPrefixLen = 8
	// maxBodyRunes caps the human `record` finding preview.
	maxBodyRunes = 200
	// minRationaleRunes is the triage-claim rationale floor.
	minRationaleRunes = 30
	// hunkFieldCount is the minimum @@-line field count.
	hunkFieldCount = 4
	// hunkStartIdx and hunkEndIdx index the @@ start/end fields.
	hunkStartIdx = 2
	hunkEndIdx   = 3
	// degenerateFloor is the minimum body length for real reviews.
	degenerateFloor = 20
	// placeholderFloor is the body length below which placeholder text refuses.
	placeholderFloor = 50
	// floatBits is the precision for threshold parsing.
	floatBits = 64
)

// jq type-rank constants for chunk index ordering.
const (
	rankNumber = 3
	rankString = 4
	rankArray  = 5
	rankObject = 6
)
