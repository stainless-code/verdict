package recordcore

const (
	fieldSources         = "sources"
	fieldClaim           = "claim"
	fieldQuote           = "quote"
	fieldConfidenceInput = "confidence"
	fieldEvidence        = "evidence"
	fieldHunk            = "hunk"
	fieldItem            = "item"
	confidenceHigh       = "high"
	fieldAnchored        = "anchored"
	fieldAt              = "at"
	fieldBody            = "body"
	fieldBrief           = "brief"
	fieldDropReason      = "dropReason"
	fieldHeading         = "heading"
	fieldHistory         = "history"
	fieldInBounds        = "inBounds"
	fieldKB              = "kb"
	fieldLastSeen        = "lastSeen"
	fieldFile            = "file"
	fieldRanges          = "ranges"
	fieldLine            = "line"
	fieldStartLine       = "start_line"
	fieldPath            = "path"
	fieldRigor           = "rigor"
	fieldSeverity        = "severity"
	fieldCategory        = "category"
	fieldSig             = "sig"
	fieldSigVersion      = "sig_version"
	fieldStatus          = "status"
	fieldSuggestion      = "suggestion"
	fieldNote            = "note"
	fieldActor           = "actor"
	fieldVerb            = "verb"
	fieldTo              = "to"
	fieldRule            = "rule"
	fieldOutcome         = "outcome"
	fieldComments        = "comments"
	fieldResolved        = "resolved"
	fieldChunks          = "chunks"
	fieldCoverage        = "coverage"
	fieldTriage          = "triage"
	fieldSide            = "side"
	fieldSecuritySummary = "security_summary"
	fieldRisk            = "risk"
	fieldLevel           = "level"
	fieldAssessedHead    = "assessed_head"
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
	schemaVersion2        = "2"
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
	outcomeAddressed      = "addressed"
	headDirty             = "dirty"
	headUnknown           = "unknown"
	tierCritical          = "critical"
	riskMedium            = "medium"
)

const (
	shortSHALen       = 7
	maxBodyRunes      = 200
	minRationaleRunes = 30
	minRiskRationale  = 10
	maxRiskRationale  = 500
	hunkFieldCount    = 4
	hunkStartIdx      = 2
	hunkEndIdx        = 3
)
