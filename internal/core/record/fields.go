package recordcore

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

const (
	schemaVersion1        = "1"
	sigVersion1           = "1"
	newline               = "\n"
	prSuffix              = "/PR.json"
	providerLocal         = "local"
	localPrefix           = "local-"
	corpusPrefix          = "corpus-"
	statusOpen            = "open"
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
