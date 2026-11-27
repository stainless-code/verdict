package schemagen

import (
	"reflect"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	ingestcore "github.com/stainless-code/verdict/internal/core/ingest"
	loopcore "github.com/stainless-code/verdict/internal/core/loop"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/repro"
	"github.com/stainless-code/verdict/internal/spend"
)

const (
	verificationSummaryType = "verificationSummary"
	finderSummaryType       = "finderSummary"
	keySchema               = "$schema"
	keyID                   = "$id"
	keyTitle                = "title"
	keyDescription          = "description"
	keyType                 = "type"
	keyProperties           = "properties"
	keyRequired             = "required"
	keyAdditionalProperties = "additionalProperties"
	keyPropertyNames        = "propertyNames"
	keyItems                = "items"
	keyAnyOf                = "anyOf"
	keyNot                  = "not"
	keyRef                  = "$ref"
	keyReceipt              = "receipt"
	keyTruncated            = "truncated"
	defsReportData          = "reportData"
	defsPostData            = "postData"
	defsSkip                = "skip"
	keyDefs                 = "$defs"
	keyConst                = "const"
	keyEnum                 = "enum"
	keyMinimum              = "minimum"
	keyMaximum              = "maximum"
	keyItemsMinimum         = "items_min"
	keyMinItems             = "minItems"
	keyMaxItems             = "maxItems"
	keyMinLength            = "minLength"
	keyMaxLength            = "maxLength"
	keyItemsEnum            = "items_enum"
	keyValueEnum            = "value_enum"
	keyExclusiveMinimum     = "exclusiveMinimum"
	keyExclusiveMin         = "exclusiveMin"
	keyNonNull              = "nonnull"
	keyFormat               = "format"
	keyPattern              = "pattern"
	typeObject              = "object"
	typeArray               = "array"
	typeString              = "string"
	typeInteger             = "integer"
	typeNumber              = "number"
	typeBoolean             = "boolean"
	typeNull                = "null"
	defsPrefix              = "#/$defs/"
	draft2020               = "https://json-schema.org/draft/2020-12/schema"
	idBase                  = "https://github.com/stainless-code/verdict/blob/main/schemas/"
	inlineTag               = "inline"
	inlineTrue              = "true"
	defsBrief               = "brief"
	defsBriefSite           = "briefSite"
	defsThread              = "thread"
	defsComment             = "comment"
	defsThreadsFile         = "threadsFile"
)

type nodeKind int

const (
	kindObject nodeKind = iota
	kindArray
	kindString
	kindNumber
	kindBool
	kindNull
)

// member is one ordered JSON object member.
type member struct { //nolint:govet // key-then-value keeps every schema literal readable
	key   string
	value *node
}

// node is a minimal ordered JSON tree; ordering is what keeps the generated
// documents byte-stable and header-first.
type node struct {
	text       string
	members    []member
	elems      []*node
	kind       nodeKind
	boolean    bool
	forceMulti bool
}

// object builds an object node. A forced object always prints multiline;
// unforced leaf objects print inline when they fit.
func object(forceMulti bool, members ...member) *node {
	return &node{kind: kindObject, members: members, forceMulti: forceMulti}
}

func array(elems ...*node) *node {
	return &node{kind: kindArray, elems: elems}
}

func stringNode(s string) *node { return &node{kind: kindString, text: s} }

func numberNode(lit string) *node { return &node{kind: kindNumber, text: lit} }

func boolNode(b bool) *node { return &node{kind: kindBool, boolean: b} }

func nullNode() *node { return &node{kind: kindNull} }

func anyNode() *node { return object(false) }

// definition registers one named struct type under a $defs name. Closed
// definitions emit additionalProperties:false; open ones (the default,
// preserving every previously published schema) emit true.
type definition struct {
	typ         reflect.Type
	name        string
	constraints []member
	// alternatives are extra whole-object shapes the definition also accepts,
	// joined to the struct's own required list through anyOf. A fetch data
	// object is the one user: a --preview run writes no bundle, so its data is
	// the preview payload and nothing else.
	alternatives []*node
	closed       bool
}

// document is one published schema: its file stem, title, root shape and
// the named definitions it references.
type document struct {
	name                 string
	title                string
	root                 reflect.Type
	anyOf                []reflect.Type
	defs                 []definition
	constraints          []member
	additionalProperties bool
}

var interfaceType = reflect.TypeFor[any]()

const defsRecord = "record"

// coverageSummaryType is the $defs name the coverage payload is published
// under; three documents reference it, so one constant keeps them in step.
const coverageSummaryType = "coverageSummary"

// chunkCoverageType is the $defs name the chunked bundle's stored attestation
// is published under on post and record.
const chunkCoverageType = "chunkCoverage"

const (
	defsValidationCounts = "validationCounts"
	defsRigorFloors      = "rigorFloors"
)

// keyScopeDefaults is the scope property carrying inline defaults; the scope
// presence rules name it more than once.
const keyScopeDefaults = "defaults"

// documents returns every published schema, in filename order.
func documents() []document {
	return []document{
		{
			name:  "author",
			title: "author.json — advisory author fingerprint",
			root:  reflect.TypeFor[AuthorFile](),
			defs: []definition{
				{typ: reflect.TypeFor[AuthorEvidence](), name: "evidence"},
			},
			additionalProperties: false,
		},
		{
			name:  "backtest",
			title: "report.json — backtest score report",
			root:  reflect.TypeFor[BacktestData](),
			defs: []definition{
				{typ: reflect.TypeFor[BacktestSpend](), name: "spend"},
				{typ: reflect.TypeFor[BacktestSignal](), name: "signal"},
			},
			additionalProperties: false,
		},
		{
			name:  "config",
			title: "config.json — typed repository configuration",
			root:  reflect.TypeFor[Config](),
			defs: []definition{
				{typ: reflect.TypeFor[ConfigDefaults](), name: "defaults", closed: true},
				{typ: reflect.TypeFor[ConfigKBPolicy](), name: "kbPolicy", closed: true},
				{typ: reflect.TypeFor[ConfigSeverityWeights](), name: "configSeverityWeights", closed: true},
				{typ: reflect.TypeFor[ConfigIndex](), name: "configIndex", closed: true},
				{typ: reflect.TypeFor[ConfigRequirements](), name: "requirements", closed: true},
				{typ: reflect.TypeFor[ConfigTrigger](), name: "trigger", closed: true},
				{typ: reflect.TypeFor[ConfigRigorPolicy](), name: "rigorPolicy", closed: true},
				{typ: reflect.TypeFor[ConfigPacks](), name: "configPacks", closed: true},
				{typ: reflect.TypeFor[ConfigOrgPack](), name: "configOrgPack", closed: true},
				{typ: reflect.TypeFor[ConfigTests](), name: "tests", closed: true},
				{typ: reflect.TypeFor[ConfigVerify](), name: "verify", closed: true},
				{typ: reflect.TypeFor[BlockClause](), name: "blockClause", closed: true},
				{typ: reflect.TypeFor[ConfigScope](), name: "scope", closed: true, constraints: []member{
					{keyAnyOf, array(
						object(false, member{keyRequired, array(stringNode("profile"))}),
						object(false, member{keyRequired, array(stringNode(keyScopeDefaults))}),
						object(false, member{keyRequired, array(stringNode("requirements"))}),
					)},
					// One not-clause carries all scope rules: profile and
					// defaults never combine, and scope defaults never carry
					// global-only keys (kb_policy, spec_coverage_min).
					{keyNot, object(false, member{keyAnyOf, array(
						object(false,
							member{keyRequired, array(stringNode("profile"), stringNode(keyScopeDefaults))}),
						object(false,
							member{keyRequired, array(stringNode(keyScopeDefaults))},
							member{keyProperties, object(false, member{keyScopeDefaults, object(false,
								member{keyRequired, array(stringNode("kb_policy"))})})},
						),
						object(false,
							member{keyRequired, array(stringNode(keyScopeDefaults))},
							member{keyProperties, object(false, member{keyScopeDefaults, object(false,
								member{keyRequired, array(stringNode("spec_coverage_min"))})})},
						),
						object(false,
							member{keyRequired, array(stringNode(keyScopeDefaults))},
							member{keyProperties, object(false, member{keyScopeDefaults, object(false,
								member{keyRequired, array(stringNode("finders"))})})}),
						object(false,
							member{keyRequired, array(stringNode(keyScopeDefaults))},
							member{keyProperties, object(false, member{keyScopeDefaults, object(false,
								member{keyRequired, array(stringNode("monthly_budget_usd"))})})}),
					)})},
				}},
				{typ: reflect.TypeFor[ConfigProfile](), name: "profile", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "controls",
			title: "controls.json — resolved review controls snapshot",
			root:  reflect.TypeFor[ControlsFile](),
			defs: []definition{
				{typ: reflect.TypeFor[ControlsFileEntry](), name: "file"},
			},
			additionalProperties: false,
		},
		{
			name:  "corpus",
			title: "corpus.json — benchmark corpus case manifest",
			root:  reflect.TypeFor[CorpusCase](),
			defs: []definition{
				{typ: reflect.TypeFor[CorpusProvenance](), name: "provenance"},
				{typ: reflect.TypeFor[CorpusBug](), name: "bug"},
			},
			additionalProperties: false,
		},
		{
			name:  "cost",
			title: "cost.json — agent self-reported spend record",
			anyOf: []reflect.Type{reflect.TypeFor[CostRecord](), reflect.TypeFor[[]CostRecord]()},
			defs: []definition{
				{typ: reflect.TypeFor[CostRecord](), name: defsRecord},
				{typ: reflect.TypeFor[spend.Phase](), name: "phaseUsage", closed: true},
				{typ: reflect.TypeFor[LensUsage](), name: "lensUsage"},
				{typ: reflect.TypeFor[GroupCost](), name: "groupCost"},
				{typ: reflect.TypeFor[ModelSpend](), name: "modelSpend"},
			},
			additionalProperties: true,
		},
		{
			name:                 "envelope",
			title:                "verdict --json envelope",
			root:                 reflect.TypeFor[Envelope](),
			defs:                 envelopeDefs(),
			additionalProperties: false,
		},
		{
			name: "excluded", title: "excluded.json — excluded diff bytes", root: reflect.TypeFor[ExcludedFile](),
			defs: []definition{{typ: reflect.TypeFor[ExcludedPath](), name: "excludedPath"}},
		},
		{
			name:  "drain",
			title: "drain.json — drain plan and receipt",
			root:  reflect.TypeFor[DrainFile](),
			defs: []definition{
				{typ: reflect.TypeFor[DrainPending](), name: "pending", closed: true},
				{typ: reflect.TypeFor[DrainSkip](), name: defsSkip, closed: true},
				{typ: reflect.TypeFor[DrainDrafts](), name: "drafts", closed: true},
				{typ: reflect.TypeFor[DrainDispositions](), name: "dispositions", closed: true},
				{typ: reflect.TypeFor[DrainPosted](), name: "posted", closed: true},
				{typ: reflect.TypeFor[DrainFailed](), name: "failed", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "drain-dryrun",
			title: "drain.dryrun.json — drain post preview",
			root:  reflect.TypeFor[DrainDryRunFile](),
			defs: []definition{
				{typ: reflect.TypeFor[DrainPlanned](), name: "planned", closed: true},
				{typ: reflect.TypeFor[DrainSkip](), name: defsSkip, closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "drain-replies",
			title: "drain.replies.json — drafted thread replies",
			root:  reflect.TypeFor[DrainRepliesFile](),
			defs: []definition{
				{typ: reflect.TypeFor[DrainReply](), name: "reply", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "findings",
			title: "findings.json — merged findings store",
			root:  reflect.TypeFor[[]FindingEntry](),
			defs: []definition{
				{typ: reflect.TypeFor[FindingEntry](), name: "entry"},
				{typ: reflect.TypeFor[core.Withheld](), name: "withheld"},
				{typ: reflect.TypeFor[core.Verification](), name: "panelVerification"},
				{typ: reflect.TypeFor[Verify](), name: "findingVerification"},
				{typ: reflect.TypeFor[core.CrossVerification](), name: "crossVerification", closed: true},
				{typ: reflect.TypeFor[core.CrossCheck](), name: "crossCheck", closed: true},
				{typ: reflect.TypeFor[core.CrossQuote](), name: "crossQuote", closed: true},
				{typ: reflect.TypeFor[repro.Receipt](), name: "reproductionReceipt", closed: true},
				{typ: reflect.TypeFor[Brief](), name: defsBrief},
				{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
				{typ: reflect.TypeFor[HistoryEvent](), name: "historyEvent"},
				{typ: reflect.TypeFor[KBCredit](), name: "kbCredit"},
			},
			additionalProperties: true,
		},
		{
			name:  "fixtures",
			title: "report.json — backtest fixtures report",
			root:  reflect.TypeFor[FixturesData](),
			defs: []definition{
				{typ: reflect.TypeFor[FixturesFixture](), name: "fixture"},
				{typ: reflect.TypeFor[FixturesRecall](), name: "recall"},
				{typ: reflect.TypeFor[FixturesBonus](), name: "bonus"},
				{typ: reflect.TypeFor[FixturesFalseGreen](), name: "falseGreen"},
				{typ: reflect.TypeFor[FixturesHallucination](), name: "hallucination"},
				{typ: reflect.TypeFor[FixturesCalibration](), name: "calibration"},
				{typ: reflect.TypeFor[FixturesDiagnostic](), name: "diagnostic"},
			},
			additionalProperties: false,
		},
		{
			name:                 "identity",
			title:                "identity.json — bundle patch identity",
			root:                 reflect.TypeFor[Identity](),
			additionalProperties: false,
		},
		{
			name:  "index",
			title: "index tree — .verdict/index/trees/<tree>.json",
			root:  reflect.TypeFor[IndexTree](),
			defs: []definition{
				{typ: reflect.TypeFor[IndexTreeFile](), name: "file"},
				{typ: reflect.TypeFor[IndexImport](), name: "import"},
				{typ: reflect.TypeFor[IndexDecl](), name: "decl"},
				{typ: reflect.TypeFor[IndexRef](), name: "ref"},
			},
			additionalProperties: false,
		},
		{
			name:  "journal",
			title: "<pr>.jsonl — append-only bundle invocation journal",
			anyOf: []reflect.Type{
				reflect.TypeFor[JournalStartLine](),
				reflect.TypeFor[JournalEndLine](),
				reflect.TypeFor[JournalTruncatedLine](),
			},
			defs: []definition{
				{typ: reflect.TypeFor[JournalStartLine](), name: "startLine", closed: true},
				{typ: reflect.TypeFor[JournalEndLine](), name: "endLine", closed: true},
				{typ: reflect.TypeFor[JournalTruncatedLine](), name: "truncatedLine", closed: true},
				{typ: reflect.TypeFor[JournalBundleDigests](), name: "bundleDigests", closed: true},
				{typ: reflect.TypeFor[JournalFileRecord](), name: "fileRecord", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "pack",
			title: "pack.json — a lens pack manifest",
			root:  reflect.TypeFor[PackManifest](),
			defs: []definition{
				{typ: reflect.TypeFor[PackLens](), name: "lens", closed: true},
				{typ: reflect.TypeFor[PackRule](), name: "rule", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "recipes",
			title: "recipes.json — declared review pipelines",
			root:  reflect.TypeFor[RecipesDocument](),
			defs: []definition{
				{typ: reflect.TypeFor[RecipeEntry](), name: "recipe", closed: true},
				{typ: reflect.TypeFor[RecipeLens](), name: "lens", closed: true},
				{typ: reflect.TypeFor[RecipeSynthesize](), name: "synthesize", closed: true},
				// The resolved pipeline the declarations compile into: the
				// published data.recipe object and launcher.json's recipe key.
				{typ: reflect.TypeFor[core.RecipePlan](), name: "plan", closed: true},
				{typ: reflect.TypeFor[core.RecipeStage](), name: "stage", closed: true},
				// The one stage a --print --stage render covers: the published
				// data.stage object, positioned into the plan's stages.
				{typ: reflect.TypeFor[RecipeStageRender](), name: "stageRender", closed: true},
				{typ: reflect.TypeFor[core.RecipeLens](), name: "lensUse", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "lock",
			title: "verdict.lock — pinned repo lens packs",
			root:  reflect.TypeFor[LockFile](),
			defs: []definition{
				{typ: reflect.TypeFor[LockPack](), name: "pack", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "post",
			title: "post.json — completed-post record (PR bundles)",
			root:  reflect.TypeFor[Post](),
			defs: []definition{
				{typ: reflect.TypeFor[ForcedPost](), name: "forcedPost"},
				{typ: reflect.TypeFor[FloorScope](), name: "floorScope"},
				{typ: reflect.TypeFor[WithheldCapability](), name: "withheldCapability"},
				{typ: reflect.TypeFor[CoverageSummary](), name: coverageSummaryType},
				{typ: reflect.TypeFor[core.PanelSummary](), name: verificationSummaryType},
				{typ: reflect.TypeFor[core.FinderSummary](), name: finderSummaryType},
				{typ: reflect.TypeFor[ChunkCoverage](), name: chunkCoverageType},
				{typ: reflect.TypeFor[core.PostSummary](), name: "postSummary"},
				{typ: reflect.TypeFor[core.RiskClaim](), name: defsRiskClaim},
				{typ: reflect.TypeFor[ApprovalReceipt](), name: defsApprovalReceipt},
				{typ: reflect.TypeFor[EvidenceReceipt](), name: defsEvidenceReceipt},
				{typ: reflect.TypeFor[EvidenceQuote](), name: defsEvidenceQuote},
				{typ: reflect.TypeFor[EvidenceReceipts](), name: defsEvidenceReceipts},
				{typ: reflect.TypeFor[EvidenceSpec](), name: defsEvidenceSpec},
				{typ: reflect.TypeFor[EvidenceDiagram](), name: defsEvidenceDiagram, closed: true},
				{typ: reflect.TypeFor[SpecCheck](), name: defsSpecCheck},
				{typ: reflect.TypeFor[SpecEvidence](), name: defsSpecEvidence},
				{typ: reflect.TypeFor[Receipt](), name: defsReceipt},
				{typ: reflect.TypeFor[ValidationCounts](), name: defsValidationCounts},
				{typ: reflect.TypeFor[RigorFloors](), name: defsRigorFloors},
			},
			additionalProperties: true,
		},

		{
			name: "calibration", title: "calibration.json — live outcome audit",
			root: reflect.TypeFor[core.Calibration](),
			defs: []definition{
				{typ: reflect.TypeFor[core.CalibrationPrecision](), name: "calibrationPrecision"},
				{typ: reflect.TypeFor[core.PrecisionCell](), name: "precisionCell"},
			},
			additionalProperties: false,
		},
		{
			name:  "record",
			title: "record.json — completed-record (local bundles)",
			root:  reflect.TypeFor[Record](),
			defs: []definition{
				{typ: reflect.TypeFor[core.AuthorRef](), name: "author"},
				{typ: reflect.TypeFor[Repair](), name: "repair"},
				{typ: reflect.TypeFor[CoverageSummary](), name: coverageSummaryType},
				{typ: reflect.TypeFor[core.PanelSummary](), name: verificationSummaryType},
				{typ: reflect.TypeFor[core.FinderSummary](), name: finderSummaryType},
				{typ: reflect.TypeFor[ChunkCoverage](), name: chunkCoverageType},
				{typ: reflect.TypeFor[core.RiskClaim](), name: "riskClaim"},
				{typ: reflect.TypeFor[ApprovalReceipt](), name: defsApprovalReceipt},
				{typ: reflect.TypeFor[EvidenceReceipt](), name: defsEvidenceReceipt},
				{typ: reflect.TypeFor[EvidenceQuote](), name: defsEvidenceQuote},
				{typ: reflect.TypeFor[EvidenceReceipts](), name: defsEvidenceReceipts},
				{typ: reflect.TypeFor[EvidenceSpec](), name: defsEvidenceSpec},
				{typ: reflect.TypeFor[EvidenceDiagram](), name: defsEvidenceDiagram, closed: true},
				{typ: reflect.TypeFor[SpecCheck](), name: defsSpecCheck},
				{typ: reflect.TypeFor[SpecEvidence](), name: defsSpecEvidence},
				{typ: reflect.TypeFor[Receipt](), name: defsReceipt},
				{typ: reflect.TypeFor[ValidationCounts](), name: defsValidationCounts},
				{typ: reflect.TypeFor[RigorFloors](), name: defsRigorFloors},
			},
			additionalProperties: true,
		},
		{
			name:  "validation",
			title: "validation.json — trusted validator pass results",
			root:  reflect.TypeFor[ValidationFile](),
			defs: []definition{
				{typ: reflect.TypeFor[ValidationResult](), name: "result", closed: true},
				{typ: reflect.TypeFor[ValidationValidator](), name: "validator", closed: true},
			},
			additionalProperties: false,
		},
		{
			name:  "panel",
			title: "verification/verdicts.json — independent verifier receipts", root: reflect.TypeFor[panelcore.File](),
			additionalProperties: true,
			defs:                 panelDefinitions(true),
		},
		{
			name:  "panel-plan",
			title: "verification/plan.json — independent dispatch plan", root: reflect.TypeFor[panelcore.PlanDocument](),
			additionalProperties: false,
			constraints:          panelPlanVersions(),
			defs:                 panelDefinitions(false),
		},

		{
			name:                 "review",
			title:                "review.json — agent-written review input",
			root:                 reflect.TypeFor[Review](),
			constraints:          reviewVersions(),
			defs:                 reviewDefs(),
			additionalProperties: false,
		},
		{
			name:                 "selected",
			title:                "selected.json — reaction-scoped fix selection",
			root:                 reflect.TypeFor[SelectedFile](),
			defs:                 nil,
			additionalProperties: false,
		},
		{
			name:  "threads",
			title: "threads.json — normalized provider review threads",
			root:  reflect.TypeFor[ThreadsFile](),
			defs: []definition{
				{typ: reflect.TypeFor[Thread](), name: "thread"},
				{typ: reflect.TypeFor[ThreadComment](), name: defsComment},
			},
			additionalProperties: false,
		},
		{
			name:  "threads-resolve",
			title: "threads-resolve.json — thread-resolve receipt",
			root:  reflect.TypeFor[ThreadsResolveReceipt](),
			defs: []definition{
				{typ: reflect.TypeFor[ThreadsResolveSkip](), name: defsSkip},
			},
			additionalProperties: false,
		},
		{
			name:  "usage",
			title: "usage.json — explicitly handed spend record",
			anyOf: []reflect.Type{reflect.TypeFor[Usage](), reflect.TypeFor[[]Usage]()},
			defs: []definition{
				{typ: reflect.TypeFor[Usage](), name: defsRecord},
				{typ: reflect.TypeFor[UsageTokens](), name: "usageTokens"},
			},
			additionalProperties: true,
		},
	}
}

func envelopeDefs() []definition {
	return []definition{
		{typ: reflect.TypeFor[core.RunData](), name: "runData"},
		{typ: reflect.TypeFor[core.RecipePlan](), name: "runRecipe"},
		{typ: reflect.TypeFor[core.RecipeStage](), name: "runRecipeStage"},
		{typ: reflect.TypeFor[core.RecipeStageRender](), name: "runRecipeStageRender"},
		{typ: reflect.TypeFor[core.RecipeLens](), name: "runRecipeLens"},
		{typ: reflect.TypeFor[core.RunExecution](), name: "runExecution"},
		{typ: reflect.TypeFor[core.RunResume](), name: "runResume"},
		{typ: reflect.TypeFor[core.RunPhaseFailure](), name: "runPhaseFailure"},
		{typ: reflect.TypeFor[core.RunRepair](), name: "runRepair"},
		{typ: reflect.TypeFor[core.ReviewBudget](), name: "reviewBudget"},
		{typ: reflect.TypeFor[core.RunSandbox](), name: "runSandbox"},
		{typ: reflect.TypeFor[core.RunAttempt](), name: "runAttempt"},
		{typ: reflect.TypeFor[core.RunEvidence](), name: "runEvidence"},
		{typ: reflect.TypeFor[core.Truncation](), name: "truncation"},
		{typ: reflect.TypeFor[SchemaError](), name: "error"},
		{typ: reflect.TypeFor[Candidate](), name: "candidate"},
		{typ: reflect.TypeFor[Problem](), name: "problem"},
		{typ: reflect.TypeFor[CheckSummary](), name: "checkSummary"},
		{typ: reflect.TypeFor[AnchorsSummary](), name: "anchorsSummary"},
		{typ: reflect.TypeFor[FindingsCounts](), name: "findingsCounts"},
		{typ: reflect.TypeFor[FindingItem](), name: "findingItem"},
		{typ: reflect.TypeFor[Provenance](), name: "provenance"},
		{typ: reflect.TypeFor[core.ProvenanceReceipt](), name: "provenanceReceipt"},
		{typ: reflect.TypeFor[core.ProvenanceInput](), name: "provenanceInput"},
		{typ: reflect.TypeFor[core.ProvenanceTruncatedRow](), name: "provenanceTruncatedRow"},
		{typ: reflect.TypeFor[CoverageSummary](), name: coverageSummaryType},
		{typ: reflect.TypeFor[core.PanelSummary](), name: verificationSummaryType},
		{typ: reflect.TypeFor[core.FinderSummary](), name: finderSummaryType},
		{typ: reflect.TypeFor[ReportData](), name: "reportData"},
		{typ: reflect.TypeFor[OrgPackInfo](), name: "orgPackInfo"},
		{typ: reflect.TypeFor[ReportHead](), name: "reportHead"},
		{typ: reflect.TypeFor[ReportVerdict](), name: "reportVerdict"},
		{typ: reflect.TypeFor[ReportCounts](), name: "reportCounts"},
		{typ: reflect.TypeFor[ReportPanel](), name: "reportPanel"},
		{typ: reflect.TypeFor[ReportFilters](), name: "reportFilters"},
		{typ: reflect.TypeFor[ReportCost](), name: "reportCost"},
		{typ: reflect.TypeFor[ReportCompare](), name: "reportCompare"},
		{typ: reflect.TypeFor[CompareBaseline](), name: "compareBaseline"},
		{typ: reflect.TypeFor[CompareCounts](), name: "compareCounts"},
		{typ: reflect.TypeFor[CompareFinding](), name: "compareFinding"},
		{typ: reflect.TypeFor[CostTiers](), name: "costTiers"},
		{typ: reflect.TypeFor[CostTier](), name: "costTier"},
		{typ: reflect.TypeFor[ReportMTD](), name: "reportMTD"},
		{typ: reflect.TypeFor[ReportCompliance](), name: "reportCompliance"},
		{typ: reflect.TypeFor[ReportReplay](), name: "reportReplay"},
		{typ: reflect.TypeFor[ReportReplaySide](), name: "reportReplaySide"},
		{typ: reflect.TypeFor[ReportReplayMove](), name: "reportReplayMove"},
		{typ: reflect.TypeFor[ReportRisk](), name: "reportRisk"},
		{typ: reflect.TypeFor[RiskInputs](), name: "riskInputs"},
		{typ: reflect.TypeFor[RiskScore](), name: "riskScore"},
		{typ: reflect.TypeFor[RiskReach](), name: "riskReach"},
		{typ: reflect.TypeFor[RiskChurn](), name: "riskChurn"},
		{typ: reflect.TypeFor[RiskCategory](), name: "riskCategory"},
		{typ: reflect.TypeFor[RiskRung](), name: "riskRung"},
		{typ: reflect.TypeFor[loopcore.Data](), name: "loopData"},
		{typ: reflect.TypeFor[loopcore.Round](), name: "loopRound"},
		{typ: reflect.TypeFor[loopcore.Fix](), name: "loopFix"},
		{typ: reflect.TypeFor[loopcore.Verify](), name: "loopVerify"},
		{typ: reflect.TypeFor[loopcore.Refusal](), name: "loopRefusal"},
		{typ: reflect.TypeFor[loopcore.PlanStep](), name: "loopPlanStep"},
		{typ: reflect.TypeFor[RiskData](), name: "riskData"},
		{typ: reflect.TypeFor[RiskBands](), name: "riskBands"},
		{typ: reflect.TypeFor[RiskRow](), name: "riskRow"},
		{typ: reflect.TypeFor[ReportGate](), name: "reportGate"},
		{typ: reflect.TypeFor[GateDimension](), name: "gateDimension"},
		{typ: reflect.TypeFor[core.RiskClaim](), name: "riskClaim"},
		{typ: reflect.TypeFor[PostData](), name: "postData"},
		{typ: reflect.TypeFor[ValidationData](), name: defsValidationData},
		{typ: reflect.TypeFor[ValidationCounts](), name: defsValidationCounts},
		{typ: reflect.TypeFor[RigorFloors](), name: defsRigorFloors},
		{typ: reflect.TypeFor[core.PostSummary](), name: "postSummary"},
		{typ: reflect.TypeFor[RecordData](), name: "recordData"},
		{typ: reflect.TypeFor[ApprovalReceipt](), name: defsApprovalReceipt},
		{typ: reflect.TypeFor[EvidenceReceipt](), name: defsEvidenceReceipt},
		{typ: reflect.TypeFor[EvidenceQuote](), name: defsEvidenceQuote},
		{typ: reflect.TypeFor[EvidenceReceipts](), name: defsEvidenceReceipts},
		{typ: reflect.TypeFor[EvidenceSpec](), name: defsEvidenceSpec},
		{typ: reflect.TypeFor[EvidenceDiagram](), name: defsEvidenceDiagram, closed: true},
		{typ: reflect.TypeFor[PostDiagram](), name: "postDiagram", closed: true},
		{typ: reflect.TypeFor[SpecCheck](), name: defsSpecCheck},
		{typ: reflect.TypeFor[SpecEvidence](), name: defsSpecEvidence},
		{typ: reflect.TypeFor[Receipt](), name: defsReceipt},
		{typ: reflect.TypeFor[Repair](), name: "repair"},
		{typ: reflect.TypeFor[ShowData](), name: "showData"},
		{typ: reflect.TypeFor[ServeData](), name: "serveData"},
		{typ: reflect.TypeFor[ServeSource](), name: "serveSource"},
		{typ: reflect.TypeFor[ServeErrorRow](), name: "serveErrorRow"},
		{typ: reflect.TypeFor[ServeRoot](), name: "serveRoot"},
		{typ: reflect.TypeFor[ServeCounts](), name: "serveCounts"},
		{typ: reflect.TypeFor[ServeRow](), name: "serveRow"},
		{typ: reflect.TypeFor[ServeArtifact](), name: "serveArtifact"},
		{typ: reflect.TypeFor[ServeCache](), name: "serveCache"},
		{typ: reflect.TypeFor[ServeSkipped](), name: "serveSkipped"},
		{typ: reflect.TypeFor[ServeDiscovery](), name: "serveDiscovery"},
		{typ: reflect.TypeFor[ServeCostData](), name: "serveCostData"},
		{typ: reflect.TypeFor[ServeCostBucket](), name: "serveCostBucket"},
		{typ: reflect.TypeFor[ServeCostTotals](), name: "serveCostTotals"},
		{typ: reflect.TypeFor[ServePrecisionData](), name: "servePrecisionData"},
		{typ: reflect.TypeFor[ServePrecisionBucket](), name: "servePrecisionBucket"},
		{typ: reflect.TypeFor[ServePrecisionTotals](), name: "servePrecisionTotals"},
		{typ: reflect.TypeFor[threadscore.File](), name: defsThreadsFile},
		{typ: reflect.TypeFor[threadscore.Thread](), name: defsThread},
		{typ: reflect.TypeFor[threadscore.Comment](), name: defsComment},
		{typ: reflect.TypeFor[ConfigData](), name: "configData"},
		{typ: reflect.TypeFor[ConfigFile](), name: "configFile"},
		{typ: reflect.TypeFor[ConfigValue](), name: "configValue"},
		{typ: reflect.TypeFor[ConfigRequirement](), name: "configRequirement"},
		{typ: reflect.TypeFor[ConfigPlanGroup](), name: "configPlanGroup"},
		{typ: reflect.TypeFor[ConfigSpend](), name: "configSpend"},
		{typ: reflect.TypeFor[ConfigCheck](), name: "configCheck"},
		{typ: reflect.TypeFor[ShowFix](), name: "showFix"},
		{typ: reflect.TypeFor[Brief](), name: defsBrief},
		{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
		{typ: reflect.TypeFor[VerifyHint](), name: "verifyHint"},
		{typ: reflect.TypeFor[FetchEstimate](), name: "fetchEstimate", constraints: []member{
			{keyAnyOf, array(
				object(false, member{keyRequired, array(stringNode("tier"))}),
				object(false, member{keyRequired, array(stringNode("groups"))}),
			)},
		}},
		{typ: reflect.TypeFor[FetchGroupEstimate](), name: "fetchGroupEstimate"},
		{typ: reflect.TypeFor[FetchGroupPersonal](), name: "fetchGroupPersonal"},
		{typ: reflect.TypeFor[TriageData](), name: "triageData"},
		{typ: reflect.TypeFor[ReadyData](), name: "readyData"},
		{typ: reflect.TypeFor[PRWaitData](), name: "prWaitData"},
		{typ: reflect.TypeFor[PRWaitSnapshot](), name: "prWaitSnapshot"},
		{typ: reflect.TypeFor[ReadyBlocker](), name: "readyBlocker"},
		{typ: reflect.TypeFor[ReadyCI](), name: "readyCI"},
		{typ: reflect.TypeFor[AdoptData](), name: "adoptData"},
		{typ: reflect.TypeFor[IngestData](), name: "ingestData"},
		{typ: reflect.TypeFor[ingestcore.Skip](), name: "ingestSkip"},
		{typ: reflect.TypeFor[NextData](), name: "nextData"},
		{typ: reflect.TypeFor[FixData](), name: "fixData"},
		{typ: reflect.TypeFor[FixAnchors](), name: "fixAnchors"},
		{typ: reflect.TypeFor[FixBatchRow](), name: "fixBatchRow"},
		{typ: reflect.TypeFor[HandoffData](), name: "handoffData"},
		{typ: reflect.TypeFor[HandoffRow](), name: "handoffRow"},
		{typ: reflect.TypeFor[JournalData](), name: "journalData"},
		{typ: reflect.TypeFor[JournalStep](), name: "journalStep"},
		{typ: reflect.TypeFor[JournalUnfinishedStep](), name: "journalUnfinishedStep"},
		{typ: reflect.TypeFor[JournalChainBreak](), name: "journalChainBreak"},
		{typ: reflect.TypeFor[JournalTruncatedInfo](), name: "journalTruncatedInfo"},
		{typ: reflect.TypeFor[JournalBundleDigests](), name: "journalBundleDigests"},
		{typ: reflect.TypeFor[JournalFileRecord](), name: "journalFileRecord"},
		{typ: reflect.TypeFor[LearnData](), name: "learnData"},
		{typ: reflect.TypeFor[LearnIncident](), name: "learnIncident"},
		{typ: reflect.TypeFor[InitData](), name: "initData"},
		{typ: reflect.TypeFor[DoctorData](), name: "doctorData"},
		{typ: reflect.TypeFor[DoctorCheck](), name: "doctorCheck"},
		{typ: reflect.TypeFor[CheckTreeData](), name: "checkTreeData"},
		{typ: reflect.TypeFor[ThreadsData](), name: "threadsData"},
		{typ: reflect.TypeFor[ThreadsResolveData](), name: "threadsResolveData"},
		{typ: reflect.TypeFor[ThreadsResolveSkip](), name: "threadsResolveSkip"},
		{typ: reflect.TypeFor[VerifyData](), name: "verifyData"},
		{typ: reflect.TypeFor[TrendData](), name: "trendData"},
		{typ: reflect.TypeFor[TrendStatuses](), name: "trendStatuses"},
		{typ: reflect.TypeFor[TrendTiers](), name: "trendTiers"},
		{typ: reflect.TypeFor[TrendRiskBands](), name: "trendRiskBands"},
		{typ: reflect.TypeFor[TrendRiskBand](), name: "trendRiskBand"},
		{typ: reflect.TypeFor[TrendPairCounts](), name: "trendPairCounts"},
		{typ: reflect.TypeFor[TrendSources](), name: "trendSources"},
		{typ: reflect.TypeFor[TrendProvenance](), name: "trendProvenance"},
		{typ: reflect.TypeFor[TrendPeriod](), name: "trendPeriod"},
		{typ: reflect.TypeFor[TrendBundleRow](), name: "trendBundleRow"},
		{typ: reflect.TypeFor[TrendIncidents](), name: "trendIncidents"},
		{typ: reflect.TypeFor[TrendIncidentBundle](), name: "trendIncidentBundle"},
		{typ: reflect.TypeFor[PackData](), name: "packData"},
		{typ: reflect.TypeFor[PackEntry](), name: "packEntry"},
		{typ: reflect.TypeFor[PackCheckData](), name: "packCheckData"},
		{typ: reflect.TypeFor[PackCheckPack](), name: "packCheckPack"},
		{typ: reflect.TypeFor[PackCheckLens](), name: "packCheckLens"},
		{typ: reflect.TypeFor[PackCheckRule](), name: "packCheckRule"},
		{typ: reflect.TypeFor[PackAddData](), name: "packAddData"},
		{typ: reflect.TypeFor[PackSyncData](), name: "packSyncData"},
		{typ: reflect.TypeFor[PackTestData](), name: "packTestData"},
		{typ: reflect.TypeFor[PackTestCase](), name: "packTestCase"},
		{typ: reflect.TypeFor[PackTestSummary](), name: "packTestSummary"},
		{typ: reflect.TypeFor[IndexData](), name: "indexData"},
		{typ: reflect.TypeFor[IndexLangRow](), name: "indexLangRow"},
		{typ: reflect.TypeFor[IndexShards](), name: "indexShards"},
		{typ: reflect.TypeFor[IndexTreeRow](), name: "indexTreeRow"},
		{typ: reflect.TypeFor[IndexRemoved](), name: "indexRemoved"},
		{typ: reflect.TypeFor[IndexQuery](), name: "indexQuery"},
		{typ: reflect.TypeFor[IndexStatus](), name: "indexStatus"},
		{typ: reflect.TypeFor[IndexCoverage](), name: "indexCoverage"},
		{typ: reflect.TypeFor[IndexCounts](), name: "indexCounts"},
		{typ: reflect.TypeFor[IndexRow](), name: "indexRow"},
		{typ: reflect.TypeFor[IndexWindow](), name: "indexWindow"},
		{typ: reflect.TypeFor[IndexTarget](), name: "indexTarget"},
		{typ: reflect.TypeFor[IndexBlastInput](), name: "indexBlastInput"},
		{typ: reflect.TypeFor[IndexBlastSymbols](), name: "indexBlastSymbols"},
		{typ: reflect.TypeFor[IndexBlastSymbol](), name: "indexBlastSymbol"},
		{typ: reflect.TypeFor[IndexBlastCallers](), name: "indexBlastCallers"},
		{typ: reflect.TypeFor[IndexBlastCaller](), name: "indexBlastCaller"},
		{typ: reflect.TypeFor[IndexBlastTests](), name: "indexBlastTests"},
		{typ: reflect.TypeFor[IndexBlastPartners](), name: "indexBlastPartners"},
		{typ: reflect.TypeFor[IndexBlastPartner](), name: "indexBlastPartner"},
		{typ: reflect.TypeFor[IndexBlastOwners](), name: "indexBlastOwners"},
		{typ: reflect.TypeFor[CalibrateData](), name: "calibrateData"},
		{typ: reflect.TypeFor[CalibrateHead](), name: "calibrateHead"},
		{typ: reflect.TypeFor[CalibratePR](), name: "calibratePR"},
		{typ: reflect.TypeFor[ProvenanceTriple](), name: "provenanceTriple"},
		{typ: reflect.TypeFor[CalibrateCounts](), name: "calibrateCounts"},
		{typ: reflect.TypeFor[CalibrateJudgment](), name: "calibrateJudgment"},
		{typ: reflect.TypeFor[core.WithheldSummary](), name: "withheldSummary"},
		{typ: reflect.TypeFor[FetchData](), name: "fetchData", alternatives: []*node{previewOnlyAlternative()}},
		{typ: reflect.TypeFor[FetchLocalData](), name: "fetchLocalData", alternatives: []*node{previewOnlyAlternative()}},
		{typ: reflect.TypeFor[FetchCapabilities](), name: "fetchCapabilities"},
		{typ: reflect.TypeFor[FetchPanel](), name: "fetchPanel"},
		{typ: reflect.TypeFor[PreviewData](), name: "previewData"},
		{typ: reflect.TypeFor[PreviewFile](), name: "previewFile"},
		{typ: reflect.TypeFor[PreviewBudget](), name: "previewBudget"},
		{typ: reflect.TypeFor[ExcludedPath](), name: "excludedPath"},
	}
}

const defsRiskClaim = "riskClaim"

// The three evidence definitions are named once: they are published in
// several documents and must keep one name everywhere.
const (
	defsApprovalReceipt  = "approvalReceipt"
	defsEvidenceReceipt  = "evidenceReceipt"
	defsEvidenceQuote    = "evidenceQuote"
	defsEvidenceReceipts = "evidenceReceipts"
	defsEvidenceSpec     = "evidenceSpec"
	defsEvidenceDiagram  = "evidenceDiagram"
	defsSpecCheck        = "specCheck"
	defsSpecEvidence     = "specEvidence"
	defsReceipt          = "receipt"
)

const defsValidationData = "validationData"

func reviewDefs() []definition {
	return []definition{
		{typ: reflect.TypeFor[core.ExecutionReference](), name: "executionReference", closed: true},
		{typ: reflect.TypeFor[Finding](), name: "finding"},
		{typ: reflect.TypeFor[SpecCheck](), name: defsSpecCheck, closed: true},
		{typ: reflect.TypeFor[SpecEvidence](), name: defsSpecEvidence, closed: true},
		{typ: reflect.TypeFor[Receipt](), name: defsReceipt, closed: true},
		{typ: reflect.TypeFor[core.Suppression](), name: "suppression"},
		{typ: reflect.TypeFor[core.Reproduction](), name: "reproduction", closed: true},
		{typ: reflect.TypeFor[Brief](), name: defsBrief},
		{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
		{typ: reflect.TypeFor[TriageClaim](), name: "triageClaim"},
		{typ: reflect.TypeFor[CoverageClaim](), name: "coverageClaim"},
		{typ: reflect.TypeFor[core.RiskClaim](), name: defsRiskClaim},
		{typ: reflect.TypeFor[ReviewDiagram](), name: "diagram", closed: true},
		{typ: reflect.TypeFor[core.DiagramNode](), name: "diagramNode", closed: true},
		{typ: reflect.TypeFor[core.DiagramEdge](), name: "diagramEdge", closed: true},
	}
}

type builder struct {
	defNames map[reflect.Type]string
}

func newBuilder(defs []definition) *builder {
	b := &builder{defNames: make(map[reflect.Type]string, len(defs))}
	for _, def := range defs {
		b.defNames[def.typ] = def.name
	}
	return b
}

func (b *builder) documentNode(doc *document) (*node, error) {
	content, err := b.rootNode(doc)
	if err != nil {
		return nil, err
	}
	members := []member{
		{keySchema, stringNode(draft2020)},
		{keyID, stringNode(idBase + doc.name + ".schema.json")},
		{keyTitle, stringNode(doc.title)},
	}
	members = append(members, content.members...)
	members = append(members, doc.constraints...)
	if len(doc.defs) > 0 {
		defs, err := b.defsNode(doc.defs)
		if err != nil {
			return nil, err
		}
		members = append(members, member{keyDefs, defs})
	}
	return object(true, members...), nil
}

func (b *builder) rootNode(doc *document) (*node, error) {
	if len(doc.anyOf) > 0 {
		return b.anyOfRoot(doc.anyOf)
	}
	if doc.root.Kind() == reflect.Struct {
		return b.structNode(doc.root, doc.additionalProperties)
	}
	return b.typeNode(doc.root, "")
}

func (b *builder) anyOfRoot(types []reflect.Type) (*node, error) {
	branches := make([]*node, 0, len(types))
	for _, typ := range types {
		branch, err := b.typeNode(typ, "")
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}
	return object(true, member{keyAnyOf, array(branches...)}), nil
}

func (b *builder) defsNode(defs []definition) (*node, error) {
	members := make([]member, 0, len(defs))
	for i := range defs {
		schema, err := b.defNode(&defs[i])
		if err != nil {
			return nil, err
		}
		members = append(members, member{defs[i].name, schema})
	}
	return object(true, members...), nil
}

// defNode renders one named definition: the struct's object, the alternative
// required sets it also accepts, its extra constraints, and the verb-wire
// members every data profile shares.
func (b *builder) defNode(def *definition) (*node, error) {
	schema, err := b.structNode(def.typ, !def.closed)
	if err != nil {
		return nil, err
	}
	if len(def.alternatives) > 0 {
		if err := requireAlternatives(schema, def.alternatives); err != nil {
			return nil, err
		}
	}
	schema.members = append(schema.members, def.constraints...)
	addVerbWireMembers(def.name, schema)
	addCompareVocabulary(def.name, schema)
	return schema, nil
}

// The compare vocabularies are published here rather than as struct tags: the
// reason family is longer than a readable field line allows, and the schema is
// the one consumer of either list.
const (
	compareStatuses = "open|addressed|wont-fix|false-positive|deferred"
	compareReasons  = "not-in-baseline|in-both|covered-and-absent|removed-from-diff|" +
		"uncovered-hunk|excluded|not-in-incremental-diff|no-location|partial-review"
)

// addCompareVocabulary attaches the published status and reason enums to the
// compareFinding definition.
func addCompareVocabulary(name string, schema *node) {
	if name != "compareFinding" {
		return
	}
	props := propertiesNode(schema)
	if props == nil {
		return
	}
	setEnum(props, "status", compareStatuses)
	setEnum(props, "reason", compareReasons)
}

// propertiesNode returns a schema object's properties node.
func propertiesNode(schema *node) *node {
	for i := range schema.members {
		if schema.members[i].key == keyProperties {
			return schema.members[i].value
		}
	}
	return nil
}

// setEnum replaces one property's node with the given enum.
func setEnum(props *node, key, spec string) {
	for i := range props.members {
		if props.members[i].key == key {
			props.members[i].value = enumNode(spec)
			return
		}
	}
}

// requireAlternatives replaces a definition's single required list with an
// anyOf of required sets: the object's own keys, or each alternative shape.
// A payload satisfying neither — an empty object, or a partial one — still
// fails, which making the keys optional could not enforce. Every branch is a
// bare required-set (plus, for an alternative, the shape that key must take),
// the form clients/ts/scripts/gen-ts-types.mjs documents: an anyOf on a node
// that declares its own shape renders the declared object, because TypeScript
// cannot express "at least one of".
func requireAlternatives(schema *node, alternatives []*node) error {
	branches := make([]*node, 0, len(alternatives)+1)
	for i := range schema.members {
		if schema.members[i].key != keyRequired {
			continue
		}
		branches = append(branches, object(false, schema.members[i]))
		branches = append(branches, alternatives...)
		schema.members[i] = member{keyAnyOf, array(branches...)}
		return nil
	}
	return errorf("a definition with alternatives needs a required list to replace")
}

// previewOnlyAlternative is the other shape a fetch data object takes: a
// --preview run stops before the bundle directory, so its data is the preview
// payload alone and no bundle key exists to require.
func previewOnlyAlternative() *node {
	return object(true,
		member{keyRequired, array(stringNode("preview"))},
		member{keyProperties, object(true, member{
			key:   "preview",
			value: object(true, member{keyRef, stringNode(defsPrefix + "previewData")}),
		})},
		member{keyAdditionalProperties, boolNode(true)},
	)
}

func (b *builder) structNode(typ reflect.Type, additional bool) (*node, error) {
	if typ.Kind() != reflect.Struct {
		return nil, errorf("%s is not a struct", typ)
	}
	props, required, err := b.fieldNodes(typ)
	if err != nil {
		return nil, err
	}
	return object(true,
		member{keyType, stringNode(typeObject)},
		member{keyProperties, props},
		member{keyRequired, required},
		member{keyAdditionalProperties, boolNode(additional)},
	), nil
}

func (b *builder) fieldNodes(typ reflect.Type) (*node, *node, error) {
	props := object(true)
	required := array()
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		if err := b.appendField(typ, &field, props, required); err != nil {
			return nil, nil, err
		}
	}
	return props, required, nil
}

func (b *builder) appendField(parent reflect.Type, field *reflect.StructField, props, required *node) error {
	if field.Tag.Get("receipt") == "true" {
		receipt, _, err := b.fieldNodes(reflect.TypeFor[core.Truncation]())
		if err != nil {
			return err
		}
		props.members = append(props.members, receipt.members...)
		return nil
	}
	prop, isRequired, skip, err := b.fieldNode(parent, field)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	props.members = append(props.members, prop)
	if isRequired {
		required.elems = append(required.elems, stringNode(prop.key))
	}
	return nil
}

func (b *builder) fieldNode(parent reflect.Type, field *reflect.StructField) (member, bool, bool, error) {
	name, optional, err := jsonFieldName(field)
	if err != nil {
		return member{}, false, false, err
	}
	if name == "" {
		return member{}, false, true, nil
	}
	schema, err := b.typeNode(field.Type, field.Tag)
	if err != nil {
		return member{}, false, false, wrapError(parent.String()+"."+field.Name, err)
	}
	if parent == reflect.TypeFor[core.Envelope]() && field.Name == "Data" {
		if schema, err = b.envelopeData(); err != nil {
			return member{}, false, false, err
		}
	}
	return member{name, schema}, !optional, false, nil
}

// envelopeData types the envelope's data slot with the optional truncation
// receipt keys every verb's data may carry.
func (b *builder) envelopeData() (*node, error) {
	props, _, err := b.fieldNodes(reflect.TypeFor[core.Truncation]())
	if err != nil {
		return nil, err
	}
	receipt, truncated := provenanceWireMembers()
	props.members = append(props.members, receipt)
	for i := range props.members {
		if props.members[i].key == keyTruncated {
			props.members[i] = truncated
		}
	}
	return object(true, member{keyType, array(stringNode("object"), stringNode("null"))},
		member{keyProperties, props}, member{keyAdditionalProperties, boolNode(true)}), nil
}

// provenanceWireMembers declares the two keys applyProvenanceWire sets when
// --provenance is active: the receipt object, and the row array that widens the
// plain boolean on data.truncated.
func provenanceWireMembers() (receipt, truncated member) {
	receipt = member{keyReceipt, object(false, member{keyRef, stringNode(defsPrefix + "provenanceReceipt")})}
	truncated = member{keyTruncated, object(false,
		member{keyType, array(stringNode(typeBoolean), stringNode(typeArray))},
		member{keyItems, object(false, member{keyRef, stringNode(defsPrefix + "provenanceTruncatedRow")})},
	)}
	return receipt, truncated
}

// addVerbWireMembers appends keys a verb writes outside its Go struct, which a
// reflected data profile cannot see.
func addVerbWireMembers(name string, schema *node) {
	if name != defsReportData && name != defsPostData {
		return
	}
	for i := range schema.members {
		if schema.members[i].key != keyProperties {
			continue
		}
		receipt, truncated := provenanceWireMembers()
		schema.members[i].value.members = append(schema.members[i].value.members, receipt, truncated)
		return
	}
}

func jsonFieldName(field *reflect.StructField) (name string, optional bool, err error) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, nil
	}
	parts := strings.Split(tag, ",")
	if len(parts) == 0 || parts[0] == "" {
		return "", false, errorf("%s.%s has no json name", field.Type, field.Name)
	}
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			optional = true
		}
	}
	if field.Tag.Get("optional") == inlineTrue {
		optional = true
	}
	return name, optional, nil
}

func (b *builder) typeNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	if schema, ok, err := b.taggedNode(typ, tag); ok || err != nil {
		return schema, err
	}
	return b.plainNode(typ, tag)
}

func (b *builder) taggedNode(typ reflect.Type, tag reflect.StructTag) (*node, bool, error) {
	if enum := tag.Get(keyEnum); enum != "" {
		return enumNode(enum), true, nil
	}
	if value := tag.Get(keyConst); value != "" {
		return object(false, member{keyConst, stringNode(value)}), true, nil
	}
	if override := tag.Get(keyType); override != "" {
		return typeListNode(override), true, nil
	}
	if union := tag.Get(keyAnyOf); union != "" {
		schema, err := b.anyOfNode(typ, union)
		return schema, true, err
	}
	if tag.Get(inlineTag) == inlineTrue {
		schema, err := b.inlineStructNode(typ)
		return schema, true, err
	}
	return nil, false, nil
}

func (b *builder) inlineStructNode(typ reflect.Type) (*node, error) {
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	return b.structNode(base, true)
}

func (b *builder) anyOfNode(typ reflect.Type, union string) (*node, error) {
	if union != typeNull {
		return nil, errorf("unsupported anyOf tag %q", union)
	}
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return nil, errorf("anyOf=null wants a struct, got %s", typ)
	}
	ref, err := b.refNode(base)
	if err != nil {
		return nil, err
	}
	return object(false, member{keyAnyOf, array(object(false, member{keyType, stringNode(typeNull)}), ref)}), nil
}

func (b *builder) plainNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	base := typ
	nullable := false
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
		// nonnull keeps an optional pointer from widening: absent reads as
		// missing through the required list, while a present null refuses.
		nullable = tag.Get(keyNonNull) != inlineTrue
	}
	if tag.Get(keyItemsMinimum) != "" && base.Kind() != reflect.Slice {
		return nil, errorf("%s: items_min needs a slice", typ)
	}
	schema, err := b.baseNode(base, tag)
	if err != nil {
		return nil, err
	}
	if nullable {
		schema, err = nullableNode(schema, base)
		if err != nil {
			return nil, err
		}
	}
	appendKeywords(schema, tag)
	return schema, nil
}

func (b *builder) baseNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	switch typ.Kind() {
	case reflect.Interface:
		if typ.NumMethod() != 0 {
			return nil, errorf("%s is not an empty interface", typ)
		}
		return anyNode(), nil
	case reflect.String:
		return object(false, member{keyType, stringNode(typeString)}), nil
	case reflect.Bool:
		return object(false, member{keyType, stringNode(typeBoolean)}), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return object(false, member{keyType, stringNode(typeInteger)}), nil
	case reflect.Float32, reflect.Float64:
		return object(false, member{keyType, stringNode(typeNumber)}), nil
	case reflect.Slice:
		return b.sliceNode(typ, tag)
	case reflect.Map:
		return b.mapNode(typ, tag)
	case reflect.Struct:
		if typ.Name() == "" {
			return b.structNode(typ, true)
		}
		return b.refNode(typ)
	default:
		return nil, errorf("unsupported Go type %s", typ)
	}
}

func (b *builder) sliceNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	elem := typ.Elem()
	if elem == interfaceType {
		if err := untypedItems(tag, typ); err != nil {
			return nil, err
		}
		return object(true, member{keyType, stringNode(typeArray)}), nil
	}
	items, err := b.itemsNode(typ, elem, tag)
	if err != nil {
		return nil, err
	}
	out := object(true, member{keyType, stringNode(typeArray)}, member{keyItems, items})
	if err := appendArrayBound(out, tag); err != nil {
		return nil, err
	}
	return out, nil
}

// appendArrayBound adds the minItems and maxItems bounds when the tags name
// them: the published array contract a parser enforces on the same field.
func appendArrayBound(out *node, tag reflect.StructTag) error {
	for _, keyword := range []string{keyMinItems, keyMaxItems} {
		value, ok, err := nonNegative(keyword, tag.Get(keyword))
		if err != nil {
			return err
		}
		if ok {
			out.members = append(out.members, member{keyword, numberNode(value)})
		}
	}
	return nil
}

// untypedItems refuses item keywords on an interface element, which has no
// item type to apply them to.
func untypedItems(tag reflect.StructTag, typ reflect.Type) error {
	for _, rule := range []struct{ keyword, reason string }{
		{keyItemsMinimum, "items_min needs item keywords"},
		{keyItemsEnum, "items_enum needs a typed element"},
		{keyMinItems, "minItems needs a typed element"},
		{keyMaxItems, "maxItems needs a typed element"},
	} {
		if tag.Get(rule.keyword) != "" {
			return errorf("%s: %s", typ, rule.reason)
		}
	}
	return nil
}

// itemsNode builds the element schema: a closed enum when the field names
// one, else the element type with the integer item keywords.
func (b *builder) itemsNode(typ reflect.Type, elem reflect.Type, tag reflect.StructTag) (*node, error) {
	if spec := tag.Get(keyItemsEnum); spec != "" {
		if minimum := tag.Get(keyItemsMinimum); minimum != "" {
			return nil, errorf("%s: items_enum and items_min do not combine", typ)
		}
		return enumNode(spec), nil
	}
	items, err := b.typeNode(elem, "")
	if err != nil {
		return nil, err
	}
	if minimum := tag.Get(keyItemsMinimum); minimum != "" {
		switch elem.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			items.members = append(items.members, member{keyMinimum, numberNode(minimum)})
		default:
			return nil, errorf("%s: items_min needs integer items", typ)
		}
	}
	return items, nil
}

// nonNegative reads an optional non-negative integer keyword value.
func nonNegative(keyword, value string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return "", false, errorf("%s %q is not a non-negative integer", keyword, value)
		}
	}
	return value, true, nil
}

func (b *builder) mapNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	if typ.Key().Kind() != reflect.String {
		return nil, errorf("map key must be a string, got %s", typ.Key())
	}
	value, err := b.mapValue(typ, tag)
	if err != nil {
		return nil, err
	}
	members := []member{{keyType, stringNode(typeObject)}, {keyAdditionalProperties, value}}
	names, named, err := mapKeyNames(tag)
	if err != nil {
		return nil, err
	}
	if named {
		members = append(members, member{keyPropertyNames, names})
	}
	return object(false, members...), nil
}

// mapValue builds the schema every map value validates against: a typed
// reference, a pinned string, or the legacy open object.
func (b *builder) mapValue(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	if ref, ok, err := b.valueRef(typ.Elem(), tag); err != nil || ok {
		if err != nil {
			return nil, err
		}
		return ref, nil
	}
	return boolNode(true), nil
}

// mapKeyNames publishes the key type when the field names one, so a schema can
// constrain a map's names (propertyNames); the pattern overlay supplies the
// grammar.
func mapKeyNames(tag reflect.StructTag) (*node, bool, error) {
	spec := tag.Get(keyPropertyNames)
	if spec == "" {
		return nil, false, nil
	}
	if spec != typeString {
		return nil, false, errorf("propertyNames %q wants %q", spec, typeString)
	}
	return object(false, member{keyType, stringNode(typeString)}), true, nil
}

// valueRef resolves a typed map value: a named struct with a $defs
// registration validates through that reference, and a string value pins the
// reason codes inside an open object. Every other element type keeps the
// legacy open object, so previously published schemas do not move.
func (b *builder) valueRef(elem reflect.Type, tag reflect.StructTag) (*node, bool, error) {
	if elem.Kind() == reflect.String {
		if enum := tag.Get(keyValueEnum); enum != "" {
			return enumNode(enum), true, nil
		}
		return object(false, member{keyType, stringNode(typeString)}), true, nil
	}
	if elem.Kind() != reflect.Struct || elem.Name() == "" {
		return nil, false, nil
	}
	if _, ok := b.defNames[elem]; !ok {
		return nil, false, nil
	}
	ref, err := b.refNode(elem)
	if err != nil {
		return nil, false, err
	}
	return ref, true, nil
}

func (b *builder) refNode(typ reflect.Type) (*node, error) {
	name, ok := b.defNames[typ]
	if !ok {
		return nil, errorf("%s has no $defs registration", typ)
	}
	return object(false, member{keyRef, stringNode(defsPrefix + name)}), nil
}

func nullableNode(schema *node, base reflect.Type) (*node, error) {
	if base.Kind() == reflect.Struct {
		return nil, errorf("nullable structs need an explicit anyOf tag")
	}
	for i, m := range schema.members {
		if m.key != keyType {
			continue
		}
		switch m.value.kind {
		case kindString:
			schema.members[i].value = array(stringNode(m.value.text), stringNode(typeNull))
		case kindArray:
			m.value.elems = append(m.value.elems, stringNode(typeNull))
		case kindObject, kindNumber, kindBool, kindNull:
			return nil, errorf("%s has a non-widenable type node", base)
		}
		return schema, nil
	}
	return nil, errorf("%s has no type to widen", base)
}

func typeListNode(spec string) *node {
	parts := strings.Split(spec, "|")
	if len(parts) == 1 {
		return object(false, member{keyType, stringNode(parts[0])})
	}
	elems := make([]*node, 0, len(parts))
	for _, part := range parts {
		elems = append(elems, stringNode(part))
	}
	return object(false, member{keyType, array(elems...)})
}

func enumNode(spec string) *node {
	parts := strings.Split(spec, "|")
	elems := make([]*node, 0, len(parts))
	for _, part := range parts {
		if part == typeNull {
			elems = append(elems, nullNode())
			continue
		}
		elems = append(elems, stringNode(part))
	}
	return object(false, member{keyEnum, array(elems...)})
}

func appendKeywords(schema *node, tag reflect.StructTag) {
	if value := tag.Get("min"); value != "" {
		schema.members = append(schema.members, member{keyMinimum, numberNode(value)})
	}
	if value := tag.Get("max"); value != "" {
		schema.members = append(schema.members, member{keyMaximum, numberNode(value)})
	}
	if value := tag.Get(keyExclusiveMin); value != "" {
		schema.members = append(schema.members, member{keyExclusiveMinimum, numberNode(value)})
	}
	if value := tag.Get(keyMaxLength); value != "" {
		schema.members = append(schema.members, member{keyMaxLength, numberNode(value)})
	}
	if value := tag.Get(keyMinLength); value != "" {
		schema.members = append(schema.members, member{keyMinLength, numberNode(value)})
	}
	if value := tag.Get(keyFormat); value != "" {
		schema.members = append(schema.members, member{keyFormat, stringNode(value)})
	}
	if value := tag.Get(keyPattern); value != "" {
		schema.members = append(schema.members, member{keyPattern, stringNode(value)})
	}
}

func panelDefinitions(receipt bool) []definition {
	if receipt {
		return []definition{
			{typ: reflect.TypeFor[panelcore.Result](), name: "result"},
			{typ: reflect.TypeFor[panelcore.CodeQuote](), name: "codeQuote", closed: true},
		}
	}
	return []definition{
		{typ: reflect.TypeFor[panelcore.DirectedCheck](), name: "directedCheck", closed: true},
		{typ: reflect.TypeFor[panelcore.ExecutionSource](), name: "executionSource", closed: true},
		{typ: reflect.TypeFor[panelcore.FinderOrigin](), name: "finderOrigin", closed: true},
		{typ: reflect.TypeFor[panelcore.SourceMerge](), name: "sourceMerge", closed: true},
		{typ: reflect.TypeFor[panelcore.Verifier](), name: "verifier"},
		{typ: reflect.TypeFor[panelcore.Finding](), name: "finding"},
		{typ: reflect.TypeFor[panelcore.Totals](), name: "totals"},
	}
}

func reviewVersions() []member {
	version := func(value string, automatic bool) member {
		properties := object(false, member{"schema_version", object(false, member{keyConst, stringNode(value)})})
		if automatic {
			properties.members = append(properties.members, member{"execution", object(false, member{keyType, stringNode(typeObject)})})
		}
		return member{keyProperties, properties}
	}
	required := member{keyRequired, array(stringNode("execution"))}
	return []member{{keyAnyOf, array(
		object(false, version("1", false), member{keyNot, object(false, required)}),
		object(false, version("2", true), required),
	)}}
}

func panelPlanVersions() []member {
	version := func(value string) member {
		return member{keyProperties, object(false,
			member{"schema_version", object(false, member{keyConst, stringNode(value)})})}
	}
	return []member{{keyAnyOf, array(
		object(false, version("1"), member{keyRequired, array(stringNode("generated_at"), stringNode("panel_source"),
			stringNode("verifiers"), stringNode("findings"), stringNode("second_look"), stringNode("dispatch"), stringNode("totals"))}),
		object(false, version("2"), member{keyRequired, array(stringNode("execution_id"), stringNode("diff_sha256"),
			stringNode("review_sha256"), stringNode("checks"), stringNode("sources"))}),
	)}}
}
