package reviewdoc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	maxMetaCommits      = 20
	maxMetaSubjectRunes = 120
	modeInitial         = "Review (initial)"
	modeLocal           = "Review (local, unposted)"
	modeRereview        = "Re-review"
	modeRereviewIncr    = "Re-review (incremental)"
	priorNone           = "none"
	dirtyLabel          = "dirty"
	unknownLabel        = "unknown"
)

// MetaCommit is one reviewed commit recorded in the metadata HTML comment.
type MetaCommit struct {
	SHA     string
	Subject string
}

// MetaFile is one reviewed file recorded in What we checked.
type MetaFile struct {
	Path      string
	Additions int
	Deletions int
	HasStats  bool
}

// BundleMeta carries bundle metadata rendered into the metadata HTML comment
// and the clean-review What we checked section.
type BundleMeta struct {
	HeadSHA     string
	HeadRef     string
	BaseSHA     string
	BaseRef     string
	Range       string
	Mode        string
	PriorReview string
	Commits     []MetaCommit
	Files       []MetaFile
	FileCount   int
	IsLocal     bool
	Dirty       bool
}

// LoadBundleMeta reads PR.json or source.json, post.json, incremental.diff,
// and full.diff from dir to populate metadata for [Render].
func LoadBundleMeta(dir, headFull string) BundleMeta {
	if prMap := readJSONMap(filepath.Join(dir, "PR.json")); prMap != nil {
		return metaFromPR(dir, headFull, prMap)
	}
	if srcMap := readJSONMap(filepath.Join(dir, "source.json")); srcMap != nil {
		return metaFromSource(dir, headFull, srcMap)
	}
	files := diffMetaFiles(dir)
	return BundleMeta{
		HeadSHA:     headFull,
		Mode:        modeInitial,
		PriorReview: priorNone,
		Files:       files,
		FileCount:   len(files),
	}
}

func metaFromPR(dir, headFull string, m map[string]any) BundleMeta {
	files := prMetaFiles(m["files"])
	if len(files) == 0 {
		files = diffMetaFiles(dir)
	}
	head := firstNonEmpty(jq.StrOf(m["headRefOid"]), jq.StrOf(m["head_sha"]), headFull)
	prior, mode := prPriorAndMode(dir, head)
	return BundleMeta{
		HeadSHA:     head,
		HeadRef:     firstNonEmpty(jq.StrOf(m["headRefName"]), jq.StrOf(m["head_ref"])),
		BaseSHA:     firstNonEmpty(jq.StrOf(m["baseRefOid"]), jq.StrOf(m["base_sha"])),
		BaseRef:     firstNonEmpty(jq.StrOf(m["baseRefName"]), jq.StrOf(m["base_ref"])),
		Mode:        mode,
		PriorReview: prior,
		Commits:     prMetaCommits(m["commits"]),
		Files:       files,
		FileCount:   len(files),
	}
}

func metaFromSource(dir, headFull string, m map[string]any) BundleMeta {
	files := sourceMetaFiles(m["files"])
	if len(files) == 0 {
		files = diffMetaFiles(dir)
	}
	dirty := m["dirty"] == true
	return BundleMeta{
		HeadSHA:     firstNonEmpty(jq.StrOf(m["head_sha"]), headFull),
		HeadRef:     jq.StrOf(m["head_ref"]),
		BaseSHA:     jq.StrOf(m["base_sha"]),
		BaseRef:     jq.StrOf(m["base_ref"]),
		Range:       jq.StrOf(m["range"]),
		Mode:        modeLocal,
		PriorReview: priorNone,
		Files:       files,
		FileCount:   len(files),
		IsLocal:     true,
		Dirty:       dirty,
	}
}

func prPriorAndMode(dir, headFull string) (prior, mode string) {
	postMap := readJSONMap(filepath.Join(dir, "post.json"))
	hasIncr := hasNonEmptyFile(filepath.Join(dir, "incremental.diff"))
	sha := postedHeadSHA(postMap)
	if !hasIncr && (sha == "" || sha == headFull) {
		return priorNone, modeInitial
	}
	prior = firstNonEmpty(readcommon.ShortSHA(sha), priorNone)
	if hasIncr {
		return prior, modeRereviewIncr
	}
	return prior, modeRereview
}

func postedHeadSHA(postMap map[string]any) string {
	if postMap == nil {
		return ""
	}
	return firstNonEmpty(jq.StrOf(postMap["head"]), jq.StrOf(postMap["head_sha"]), jq.StrOf(postMap["commit_id"]))
}

func prMetaCommits(raw any) []MetaCommit {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]MetaCommit, 0, len(items))
	for _, item := range items {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sha := firstNonEmpty(jq.StrOf(cm["oid"]), jq.StrOf(cm["sha"]))
		subj := firstLine(firstNonEmpty(jq.StrOf(cm["messageHeadline"]), jq.StrOf(cm["subject"]), jq.StrOf(cm["message"])))
		if sha == "" && subj == "" {
			continue
		}
		out = append(out, MetaCommit{SHA: sha, Subject: subj})
	}
	return out
}

func prMetaFiles(raw any) []MetaFile {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]MetaFile, 0, len(items))
	for _, item := range items {
		fm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p := jq.StrOf(fm["path"])
		if p == "" {
			continue
		}
		adds, _ := jq.IntOf(fm["additions"])
		dels, _ := jq.IntOf(fm["deletions"])
		out = append(out, MetaFile{Path: p, Additions: adds, Deletions: dels, HasStats: true})
	}
	return out
}

func sourceMetaFiles(raw any) []MetaFile {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]MetaFile, 0, len(items))
	for _, item := range items {
		if f, ok := parseSourceFile(item); ok {
			out = append(out, f)
		}
	}
	return out
}

func parseSourceFile(item any) (MetaFile, bool) {
	if s, ok := item.(string); ok && s != "" {
		return MetaFile{Path: s}, true
	}
	fm, ok := item.(map[string]any)
	if !ok {
		return MetaFile{}, false
	}
	p := jq.StrOf(fm["path"])
	if p == "" {
		return MetaFile{}, false
	}
	adds, hasA := jq.IntOf(fm["additions"])
	dels, hasD := jq.IntOf(fm["deletions"])
	return MetaFile{Path: p, Additions: adds, Deletions: dels, HasStats: hasA || hasD}, true
}

func diffMetaFiles(dir string) []MetaFile {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "full.diff"))
	if err != nil {
		return nil
	}
	sections := hunks.Files(string(raw))
	out := make([]MetaFile, 0, len(sections))
	for i := range sections {
		out = append(out, MetaFile{Path: sections[i].Path})
	}
	return out
}

func renderMetadataComment(meta *BundleMeta, headFull string) string {
	headShort := metaHeadShort(meta, headFull)
	mode := firstNonEmpty(meta.Mode, modeInitial)
	prior := firstNonEmpty(meta.PriorReview, priorNone)
	fileCount := meta.FileCount
	if fileCount == 0 {
		fileCount = len(meta.Files)
	}
	lines := []string{
		"<!--",
		fmt.Sprintf("Review metadata. Findings written against %s;", untrusted.HTMLCommentLine(headShort)),
		staleWarningLine(meta.HeadRef),
		"- Mode: " + untrusted.HTMLCommentLine(mode),
		"- Files reviewed: " + strconv.Itoa(fileCount),
	}
	lines = appendMetaScope(lines, meta)
	if base := formatRefSHA(meta.BaseRef, meta.BaseSHA); base != "" {
		lines = append(lines, "- Base: "+base)
	}
	lines = append(lines, "- Head: "+formatHeadRefSHA(meta.HeadRef, headShort))
	lines = appendMetaCommits(lines, meta)
	lines = append(lines, "- Prior review: "+untrusted.HTMLCommentLine(prior), "-->")
	return strings.Join(lines, "\n")
}

func metaHeadShort(meta *BundleMeta, headFull string) string {
	headSHA := firstNonEmpty(meta.HeadSHA, headFull)
	if meta.IsLocal && (meta.Dirty || headSHA == "") {
		return dirtyLabel
	}
	if s := readcommon.ShortSHA(headSHA); s != "" {
		return s
	}
	return unknownLabel
}

func staleWarningLine(headRef string) string {
	if clean := untrusted.HTMLCommentLine(headRef); clean != "" {
		return fmt.Sprintf("if commits landed on %s since, treat every bug/file/line as POTENTIALLY STALE.", clean)
	}
	return "if commits landed since, treat every bug/file/line as POTENTIALLY STALE."
}

func appendMetaScope(lines []string, meta *BundleMeta) []string {
	if meta.IsLocal {
		if rng := untrusted.HTMLCommentLine(meta.Range); rng != "" {
			lines = append(lines, "- Range: "+rng)
		}
		return lines
	}
	return append(lines, "- Commits reviewed: "+strconv.Itoa(len(meta.Commits)))
}

func appendMetaCommits(lines []string, meta *BundleMeta) []string {
	if meta.IsLocal || len(meta.Commits) == 0 {
		return lines
	}
	lines = append(lines, "- Reviewed commits:")
	shown := min(len(meta.Commits), maxMetaCommits)
	for i := range shown {
		lines = append(lines, formatCommitLine(meta.Commits[i]))
	}
	if len(meta.Commits) > maxMetaCommits {
		lines = append(lines, fmt.Sprintf("  - (+%d more)", len(meta.Commits)-maxMetaCommits))
	}
	return lines
}

func formatCommitLine(c MetaCommit) string {
	short := untrusted.HTMLCommentLine(firstNonEmpty(readcommon.ShortSHA(c.SHA), unknownLabel))
	subj := untrusted.HTMLCommentLine(untrusted.CapRunes(c.Subject, maxMetaSubjectRunes))
	if subj == "" {
		return "  - " + short
	}
	return "  - " + short + " — " + subj
}

func formatRefSHA(ref, sha string) string {
	cleanRef := untrusted.HTMLCommentLine(ref)
	short := untrusted.HTMLCommentLine(readcommon.ShortSHA(sha))
	switch {
	case cleanRef != "" && short != "":
		return cleanRef + " (" + short + ")"
	case cleanRef != "":
		return cleanRef
	default:
		return short
	}
}

func formatHeadRefSHA(ref, headShort string) string {
	cleanRef := untrusted.HTMLCommentLine(ref)
	cleanSHA := untrusted.HTMLCommentLine(headShort)
	if cleanRef != "" {
		return cleanRef + " (" + cleanSHA + ")"
	}
	return cleanSHA
}

func readJSONMap(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, _ := jq.AsMap(doc)
	return m
}

func hasNonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	if head, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(head)
	}
	return strings.TrimSpace(s)
}
