package fetchcore

// Shared literals for the fetch port: jq wire keys, bundle path suffixes,
// provider argv tokens, refusal codes, and numeric gates. The shell repeats
// these inline; Go names them once (revive add-constant, goconst).
const (
	// JSON wire keys (jq object fields).
	keyAdds        = "adds"
	keyAdditions   = "additions"
	keyAuthor      = "author"
	keyBaseRef     = "base_ref"
	keyBaseSHA     = "base_sha"
	keyBody        = "body"
	keyCommitID    = "commitId"
	keyData        = "data"
	keyDels        = "dels"
	keyDeletions   = "deletions"
	keyDescription = "description"
	keyDraft       = "draft"
	keyEstimated   = "estimated_bytes"
	keyFiles       = "files"
	keyFilename    = "filename"
	keyHeadRef     = "head_ref"
	keyHeadSHA     = "head_sha"
	keyID          = "id"
	keyIndex       = "index"
	keyItems       = "items"
	keyJSON        = "json"
	keyKind        = "kind"
	keyLogin       = "login"
	keyNumber      = "number"
	keyPath        = "path"
	keyPR          = "pr"
	keyProvider    = "provider"
	keySchema      = "schema_version"
	keySource      = "source"
	keyState       = "state"
	keyStatus      = "status"
	keySuggestion  = "suggestion"
	keyTitle       = "title"
	keyURL         = "url"

	// Bundle path suffixes (dir + suffix, like record's prSuffix).
	suffixChunks        = "/chunks/"
	suffixChunkManifest = "/chunks/manifest.json"
	suffixDiffErr       = "/.diff.err"
	suffixFullDiff      = "/full.diff"
	suffixInlineJSON    = "/inline.json"
	suffixKBRecall      = "/kb-recall.json"
	suffixKBTxt         = "/kb.txt"
	suffixMetaErr       = "/.meta.err"
	suffixPRJSON        = "/PR.json"
	suffixReviewsJSON   = "/reviews.json"

	// Provider binaries and argv tokens.
	binAz        = "az"
	binGit       = "git"
	binGlab      = "glab"
	ghCmdPR      = "pr"
	flagH        = "-H"
	flagOut      = "-o"
	flagQuery    = "--query"
	flagCached   = "--cached"
	flagChunk    = "--chunk"
	flagCurlFail = "-sf"
	flagJSON     = "--json"
	flagPaginate = "--paginate"
	flagRepo     = "-R"
	flagVerify   = "--verify"

	// Subcommands and git nouns.
	gitCmdDiff     = "diff"
	gitCmdRevParse = "rev-parse"
	gitHeadRef     = "HEAD"
	gitRange3      = "..."
	modeBranch     = "branch"
	modeRange      = "range"
	modeStash      = "stash"
	modeStaged     = "staged"

	// Providers, states, and misc values.
	providerCafe   = "cafe"
	providerGitHub = "github"
	stateClosed    = "closed"
	stateMerged    = "merged"
	stateOpen      = "open"
	stateUnknown   = "unknown"
	statusModified = "modified"
	noteKindDiff   = "DiffNote"
	tierStandard   = "standard"
	nullStr        = "null"
	strTrue        = "true"
	devNullPath    = "/dev/null"
	emptyJSONArray = "[]\n"
	newline        = "\n"
	dblQuote       = "\""
	pathSep        = "/"

	// Refusal codes.
	codeAuthMissing = "auth-missing"
	codeBadRange    = "bad-range"
	codeBadSlug     = "bad-slug"

	// Messages and message formats.
	msgUnreadablePR  = "unreadable %s/PR.json (re-fetch the PR)"
	msgWriteFullDiff = "could not write %s/full.diff: %v"
	msgWriteInline   = "could not write %s/inline.json: %v"
	msgWriteReviews  = "could not write %s/reviews.json: %v"
	noChunkRef       = "(no readable chunk diff ref)"

	// Numeric gates.
	numBase10       = 10
	numBitSize      = 64
	numExcerpt      = 200
	numExcerptShort = 120
	numRoundScale   = 10000
	numShownHits    = 5
	numShownPaths   = 10
	numSlugMax      = 48
	numStatFields   = 3
	numTopRules     = 3
	repoParts       = 3
	cafeMaxPages    = 20
	rankNumber      = 3
	rankString      = 4
	rankArray       = 5
	rankOther       = 6
	recallBaseScore = 0.5
	dirPerm         = 0o755
	gitConfigMode   = 0o644
)

// Additional argv and schema constants (revive add-constant).
const (
	ghCmdAPI       = "api"
	apiReposPrefix = "repos/"
	stashRefPrefix = "stash@{"
	spaceSep       = " "
	schemaV1       = "1"
	roundHalf      = 0.5
	diffSuffix     = ".diff"
)
