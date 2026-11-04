package fetchcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// pageFileList fills in a truncated file list: a list arriving exactly
// at the hosting CLI cap pages the provider's pull-request file listing
// (up to the provider ceiling), and the bundle notes which ceiling
// applied. Coverage inputs downstream read PR.json, so the paged list
// replaces the truncated files array in place; filesource.json records the
// source and ceiling. Below the cap nothing changes: no extra calls, no
// extra files, byte-identical bundles. A failed page warns loudly and
// keeps the CLI list instead of covering silently.
func pageFileList(ctx context.Context, ctx2 *fetchRunCtx, dir, repo, pr string) error {
	files, ordered, ok := prFilesOrdered(dir + suffixPRJSON)
	if !ok || len(files) != cliFileCap {
		return nil
	}
	paged, truncated, perr := fetchAllFiles(ctx, ctx2.runner, repo, pr)
	if perr != nil {
		ctx2.runner.Warn(fmt.Sprintf("file-list paging failed (%s) — proceeding with the CLI's first %d files "+
			"(under-coverage: the checklist may miss files past the CLI cap)",
			headBytes(perr.Error(), numExcerptShort), len(files)))
		note := jq.NewObj(
			keySource, "cli",
			"cli_cap", cliFileCap,
			"provider_ceiling", providerFileCeiling,
			"count", len(files),
			"paging_error", headBytes(perr.Error(), numExcerptShort),
		)
		if err := atomic.WriteFile(dir+"/filesource.json", []byte(jq.PrettyOrdered(note))); err != nil {
			return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/filesource.json: %v", dir, err)}
		}
		return nil
	}
	replaceFiles(ordered, paged)
	if err := atomic.WriteFile(dir+suffixPRJSON, []byte(jq.PrettyOrdered(ordered))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/PR.json: %v", dir, err)}
	}
	note := jq.NewObj(
		keySource, "paged",
		"cli_cap", cliFileCap,
		"provider_ceiling", providerFileCeiling,
		"count", len(paged),
		"truncated", truncated,
	)
	if err := atomic.WriteFile(dir+"/filesource.json", []byte(jq.PrettyOrdered(note))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/filesource.json: %v", dir, err)}
	}
	fmt.Fprintf(ctx2.runner.Human, "» file list paged past the CLI cap (%d files, provider ceiling %d)\n", len(paged), providerFileCeiling)
	return nil
}

// prFilesOrdered reads PR.json's files with the decoded document preserved
// for an in-place rewrite (ok=false when the record is unreadable).
func prFilesOrdered(prJSON string) ([]any, *jq.Obj, bool) {
	raw, err := os.ReadFile(prJSON)
	if err != nil {
		return nil, nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, nil, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return nil, nil, false
	}
	v, _ := obj.Get(keyFiles)
	items, ok := v.([]any)
	if !ok {
		return nil, nil, false
	}
	return items, obj, true
}

// fetchAllFiles pages the provider file listing to exhaustion or the
// provider ceiling, mapping each entry onto the PR.json file shape.
func fetchAllFiles(ctx context.Context, r *session, repo, pr string) ([]any, bool, error) {
	res, err := r.ghFetch(ctx, "api", "repos/"+repo+"/pulls/"+pr+"/files", flagPaginate)
	if err != nil {
		return nil, false, err
	}
	docs, derr := jq.DecodeSlurp(strings.NewReader(res.Stdout))
	if derr != nil {
		return nil, false, internalErrorf("provider file listing unreadable: %v", derr)
	}
	var out []any
	for _, doc := range docs {
		arr, ok := doc.([]any)
		if !ok {
			return nil, false, internalErrorf("provider file listing unreadable")
		}
		var truncated bool
		out, truncated = appendFileEntries(out, arr)
		if truncated {
			return out, true, nil
		}
	}
	return out, false, nil
}

// appendFileEntries maps one listing page, reporting the ceiling cut.
func appendFileEntries(out []any, arr []any) ([]any, bool) {
	for _, e := range arr {
		out = append(out, mapFileEntry(e))
		if len(out) >= providerFileCeiling {
			return out, true
		}
	}
	return out, false
}

// mapFileEntry maps one provider file-listing entry onto the PR.json file
// shape: path from filename, numeric stats defaulting to zero, rename and
// status context kept when the provider names them.
func mapFileEntry(e any) any {
	m := objOrMap(e)
	if m == nil {
		return jq.NewObj(keyPath, jq.R(e), keyAdditions, 0, keyDeletions, 0)
	}
	obj := jq.NewObj(
		keyPath, fileNameOf(m),
		keyAdditions, fileNumOf(m[keyAdditions]),
		keyDeletions, fileNumOf(m[keyDeletions]),
	)
	setFileStatus(obj, m)
	setFilePrevName(obj, m)
	return obj
}

// setFileStatus keeps the provider status when it is a non-empty string,
// rendering anything else through jq.
func setFileStatus(obj *jq.Obj, m map[string]any) {
	status, ok := m[keyStatus]
	if !ok || status == nil {
		return
	}
	if s, ok := status.(string); ok && s != "" {
		obj.Set(keyStatus, s)
		return
	}
	obj.Set(keyStatus, jq.R(status))
}

// setFilePrevName keeps a non-empty previous filename when present.
func setFilePrevName(obj *jq.Obj, m map[string]any) {
	prev, ok := m["previous_filename"]
	if !ok || prev == nil {
		return
	}
	if s, ok := prev.(string); ok && s != "" {
		obj.Set("previous_filename", s)
	}
}

// objFields flattens an ordered object into a plain map.

// objOrMap normalizes a decoded JSON value to a plain field map: plain
// objects pass through, ordered objects flatten, and anything else reads
// nil.
func objOrMap(e any) map[string]any {
	if m, ok := e.(map[string]any); ok {
		return m
	}
	if om, ok := e.(*jq.Obj); ok {
		return objFields(om)
	}
	return nil
}

func objFields(o *jq.Obj) map[string]any {
	m := map[string]any{}
	for _, k := range o.Keys {
		v, _ := o.Get(k)
		m[k] = v
	}
	return m
}

// fileNameOf reads filename/path from a provider entry ("" when absent).
func fileNameOf(m map[string]any) string {
	if s := jq.StrOf(m[keyFilename]); s != "" {
		return s
	}
	return jq.R(m[keyPath])
}

// fileNumOf reads a numeric stat, defaulting to zero like `// 0`.
func fileNumOf(v any) any {
	switch t := v.(type) {
	case jq.Number:
		return t
	case json.Number:
		return jq.Number(t.String())
	case float64:
		return t
	case int:
		return t
	default:
		return 0
	}
}

// replaceFiles swaps the files array in place and recomputes the summed
// stats from the paged list.
func replaceFiles(ordered *jq.Obj, paged []any) {
	ordered.Set(keyFiles, paged)
	var adds, dels float64
	for _, e := range paged {
		var m map[string]any
		if om, ok := e.(*jq.Obj); ok {
			m = objFields(om)
		} else if pm, ok := e.(map[string]any); ok {
			m = pm
		} else {
			continue
		}
		adds += readcommon.NumOr(m[keyAdditions])
		dels += readcommon.NumOr(m[keyDeletions])
	}
	ordered.Set(keyAdditions, adds)
	ordered.Set(keyDeletions, dels)
}
