package fetchcore

// Shared literals for the fetch verbs: jq wire keys, bundle path suffixes,
// provider argv tokens, refusal codes, and numeric gates, each named once so
// no call site repeats a literal.
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
	keyIssue       = "issue"
	keyItems       = "items"
	keyJSON        = "json"
	keyKind        = "kind"
	keyLabels      = "labels"
	keyLogin       = "login"
	keyName        = "name"
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

	// fullDiffFile names the whole-diff artifact in next-step text and dumps.
	fullDiffFile = "full.diff"

	// planFile names the combined plan section inside the prompt dump.
	planFile = "plan.txt"

	suffixChunks        = "/chunks/"
	suffixChunkManifest = "/chunks/manifest.json"
	suffixDiffErr       = "/.diff.err"
	suffixFullDiff      = "/full.diff"
	suffixRawDiff       = "/full.raw.diff"
	suffixIdentity      = "/identity.json"
	suffixInlineJSON    = "/inline.json"
	suffixKBRecall      = "/kb-recall.json"
	suffixKBTxt         = "/kb.txt"
	suffixLensesTxt     = "/lenses.txt"
	suffixRevertsTxt    = "/reverts.txt"
	suffixMetaErr       = "/.meta.err"
	suffixProcedureMD   = "/procedure.md"
	suffixPRJSON        = "/PR.json"
	suffixReviewsJSON   = "/reviews.json"
	suffixThreadsJSON   = "/threads.json"

	// Provider binaries and argv tokens.
	binAz              = "az"
	binCurl            = "curl"
	binGit             = "git"
	binGlab            = "glab"
	ghCmdPR            = "pr"
	flagH              = "-H"
	flagOut            = "-o"
	flagQuery          = "--query"
	flagCached         = "--cached"
	flagChunk          = "--chunk"
	flagCurlDisable    = "--disable"
	flagCurlFail       = "-sf"
	flagCurlFollowFail = "-sfL"
	// flagCurlPublicFail is the anonymous read's flag set: -S keeps curl's own
	// error line on stderr, which is the only text a 403, 404, or 429 leaves
	// behind (a silent -s turns every one of them into an empty failure).
	flagCurlPublicFail = "-sSfL"
	flagDetach         = "--detach"
	flagForce          = "--force"
	flagJSON           = "--json"
	flagPaginate       = "--paginate"
	flagRepo           = "-R"
	flagResource       = "--resource"
	flagVerify         = "--verify"

	// Subcommands and git nouns.
	gitCmdDiff        = "diff"
	gitCmdRevParse    = "rev-parse"
	gitCmdWorktree    = "worktree"
	gitCmdCommitTree  = "commit-tree"
	gitCmdStash       = "stash"
	gitCmdStatus      = "status"
	gitWorktreeAdd    = "add"
	gitWorktreeRemove = "remove"
	gitStashCreate    = "create"
	gitDashC          = "-C"
	gitEmptyTree      = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	gitHeadRef        = "HEAD"
	gitRange3         = "..."
	modeBranch        = "branch"
	modeRange         = "range"
	modeStash         = "stash"
	modeStaged        = "staged"

	// Providers, states, and misc values.
	providerAzure     = "azure"
	providerBitbucket = "bitbucket"
	providerCafe      = "cafe"
	providerForgejo   = "forgejo"
	providerGitCafe   = "gitcafe"
	providerGitHub    = "github"
	providerGitLab    = "gitlab"
	gitlabProjects    = "projects/"
	stateActive       = "active"
	stateClosed       = "closed"
	stateMerged       = "merged"
	stateOpen         = "open"
	stateUnknown      = "unknown"
	statusModified    = "modified"
	noteKindDiff      = "DiffNote"

	// Thread feed keys and side labels.
	keyLine        = "line"
	keySide        = "side"
	sideLeft       = "LEFT"
	sideRight      = "RIGHT"
	tierStandard   = "standard"
	nullStr        = "null"
	strTrue        = "true"
	devNullPath    = "/dev/null"
	emptyJSONArray = "[]\n"
	newline        = "\n"
	dblQuote       = "\""
	pathSep        = "/"

	// Refusal codes.
	codeAuthMissing    = "auth-missing"
	codeBadProvider    = "bad-provider"
	codeThreadsFailed  = "threads-failed"
	codeBadRange       = "bad-range"
	codeBadSlug        = "bad-slug"
	codeWorktreeFailed = "worktree-failed"

	// Messages and message formats.
	msgUnreadablePR   = "unreadable %s/PR.json (re-fetch the PR)"
	msgWorktreeCommit = "verdict fetch-local --worktree target"
	msgWriteFullDiff  = "could not write %s/full.diff: %v"
	msgWriteInline    = "could not write %s/inline.json: %v"
	msgWriteReviews   = "could not write %s/reviews.json: %v"
	noChunkRef        = "(no readable chunk diff ref)"

	// Numeric gates.
	numBase10           = 10
	numBitSize          = 64
	numExcerpt          = 200
	numExcerptShort     = 120
	threadBodyExcerpt   = 400
	numShownHits        = 5
	numShownPaths       = 10
	numSlugMax          = 48
	numStatFields       = 3
	numTopRules         = 3
	numWorktreeSuffixes = 99
	repoParts           = 3
	cafeMaxPages        = 20
	recallBaseScore     = 0.5
	dirPerm             = 0o755
	gitConfigMode       = 0o644
)

const (
	ghCmdAPI       = "api"
	apiReposPrefix = "repos/"
	stashRefPrefix = "stash@{"
	spaceSep       = " "
	schemaV1       = "1"
	diffSuffix     = ".diff"
)

// Public GitHub reads (verdict demo's URL form): the anonymous REST root, the
// diff media type, the list page size, the files sub-resource and its receipt,
// the anonymous hourly request budget, and the one warning the token-only
// thread read emits.
const (
	publicAPIHost        = "https://api.github.com/"
	publicDiffAccept     = "Accept:application/vnd.github.v3.diff"
	publicFilesPath      = "/files"
	publicReviewsPath    = "/reviews"
	publicCommentsPath   = "/comments"
	publicFilesSource    = "api"
	publicFilesourceFile = "/filesource.json"
	msgPublicThreads     = "thread resolution needs 'gh auth login'; this public bundle records none"
)

const (
	// publicPerPage is the list page size the anonymous API is asked for.
	publicPerPage = 100
	// publicRateLimit is the anonymous hourly request budget per IP.
	publicRateLimit = 60
)

// Issue sources in the context set: the count cap on auto-discovered issues,
// the per-document byte cap, and the one GraphQL page size.
const (
	issueMaxLinked  = 5
	issueMaxBytes   = 64 << 10
	issueMaxGraphQL = 20
	issueExt        = ".md"
)

// Issue reference kinds: the two shapes --issue resolves through.
const (
	issueKindBare   = "bare"
	issueKindGitHub = "github"
)

// Issue warning reasons. Each names one outcome class a read can reach; none
// of them ever carries a response body or a credential.
const (
	reasonHostError      = "host-error"
	reasonMissingTool    = "missing-tool"
	reasonNotFound       = "not-found"
	reasonOverBytes      = "bytes"
	reasonOverFiles      = "files"
	reasonOverItems      = "items"
	reasonOverIssues     = "issues"
	reasonTruncatedBytes = "bytes"
	reasonTruncatedList  = "list"
	reasonUnauthorized   = "unauthorized"
	reasonUnsupported    = "unsupported"
	reasonRateLimited    = "rate-limited"
)

// Issue surface messages: the refusal texts --issue refuses with and the
// typed warnings it emits, each with its token and reason spelled once.
const (
	msgIssueWhitespace      = "--issue %s: whitespace is not an issue reference"
	msgIssueUnrecognized    = "--issue %s: unrecognized issue reference (want owner/repo#12, #12, or a GitHub issue URL)"
	msgIssueNeedsNumber     = "--issue %s: an issue reference needs a number"
	msgIssueBareProvider    = "--issue %s: a bare #N needs --provider github (name the repo as owner/repo#N)"
	msgIssueUnavailable     = "issue-unavailable (%s): %s"
	msgIssueTruncated       = "issue-truncated (%s): %s"
	msgIssueOverCap         = "issue-over-cap (%s): %s"
	msgIssueOverIssues      = "issue-over-cap (%s): kept %d of %d linked issues"
	msgIssueUnsupported     = "issue-unavailable (%s): %s has no linked-issue read in this build (link the issue with --issue)"
	msgIssueLinkedReadError = "%s#%s linked issues"
)
