package recordcore

const (
	fieldAnchored   = "anchored"
	fieldAt         = "at"
	fieldBody       = "body"
	fieldBrief      = "brief"
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
	fieldCategory   = "category"
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
	fieldComments   = "comments"
	fieldResolved   = "resolved"
	fieldChunks     = "chunks"
	fieldCoverage   = "coverage"
	fieldSections   = "sections"
	fieldTriage     = "triage"
	fieldSide       = "side"
	fieldConfidence = "confidence"
	// fieldInBoundsInput is the agent-facing hyphenated key; fieldInBounds
	// is the store's camelCase key.
	fieldInBoundsInput   = "in-bounds"
	fieldSecuritySummary = "security_summary"
	// fieldRigorSource is the provenance of the recorded rung.
	fieldRigorSource = "rigor_source"
)

const (
	rungSaid       = "said"
	rungPointed    = "pointed"
	rungWalked     = "walked"
	rungRan        = "ran"
	rungReproduced = "reproduced"

	rigorSourceModel   = "model"
	rigorSourceDefault = "default"
)

const (
	schemaVersion1        = "1"
	sigVersion1           = "1"
	newline               = "\n"
	prSuffix              = "/PR.json"
	reviewSuffix          = "/review.json"
	sourceSuffix          = "/source.json"
	diffSuffix            = "/full.diff"
	providerLocal         = "local"
	localPrefix           = "local-"
	corpusPrefix          = "corpus-"
	statusOpen            = "open"
	sideLeft              = "LEFT"
	sideRight             = "RIGHT"
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
	sigNone               = "NONE"
	sigMany               = "MANY"
	chunkHunksSuffix      = "/.chunk-hunks.tsv"
	revalidatorForced     = "revalidator"
	fieldEvent            = "event"
	fieldSHA              = "sha"
	fieldStartLine        = "start_line"
	outcomeAddressed      = "addressed"
)

const (
	shortSHALen       = 7
	maxBodyRunes      = 200
	minRationaleRunes = 30
	hunkFieldCount    = 4
	hunkStartIdx      = 2
	hunkEndIdx        = 3
	degenerateFloor   = 20
	placeholderFloor  = 50
)
