package fetchcore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestChunkReceipt(t *testing.T) {
	t.Parallel()
	plan := []chunkEntry{{name: "001", members: []fileSlice{{path: "a.go"}}}, {name: "002", members: []fileSlice{{path: "b.go"}}}}
	rows := chunksJSON(t.TempDir(), plan)
	first, ok := rows[0].(*jq.Obj)
	if !ok {
		t.Fatal("chunk is not an object")
	}
	want := `{"truncated":true,"truncation_reason":"chunk_cap","returned":1,"total":2,"next_offset":1}`
	got := jq.NewObj()
	for _, key := range []string{"truncated", "truncation_reason", "returned", "total", "next_offset"} {
		v, _ := first.Get(key)
		got.Set(key, v)
	}
	if got.MarshalCompact() != want {
		t.Fatalf("chunk receipt = %s, want %s", got.MarshalCompact(), want)
	}
}

func TestImpactSectionCapDoesNotTruncateImpactJSONOrBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "toc.txt"), []byte("# TOC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	impact := impactFixture()
	widenBlast(impact.Data.Blast)
	ctx2, _, _ := impactRunCtx(t, dir)
	if err := writeImpactFiles(ctx2, impact); err != nil {
		t.Fatal(err)
	}
	doc := impactJSON(impact)
	if v, ok := doc.Get("truncated"); ok {
		t.Fatalf("impact.json truncated = %v, want omitted when only toc.txt section capped", v)
	}
	data := &core.FetchData{Dir: dir, Files: 3}
	if err := finishTruncation(data); err != nil {
		t.Fatal(err)
	}
	if data.Receipt != nil || readcommon.BundleTruncation(dir) != nil {
		t.Fatalf("bundle receipt = %+v / %+v, want nil when only impact section or sub-list is capped", data.Receipt, readcommon.BundleTruncation(dir))
	}
}

func TestFileListReceipt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := strings.Repeat(`{"path":"a.go"},`, 99) + `{"path":"last.go"}`
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"files":[`+files+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{"gh\x00api\x00repos/o/r/pulls/7/files\x00--paginate": {fail: true}}}
	ctx := hostCtx(t, cmdr, dir)
	if err := pageFileList(context.Background(), ctx, dir, "o/r", "7"); err != nil {
		t.Fatal(err)
	}
	receipt := readcommon.BundleTruncation(dir)
	if receipt == nil || receipt.TruncationReason != "scan_cap" || receipt.Returned != 100 || receipt.Total != nil {
		t.Fatalf("file receipt = %+v", receipt)
	}
}

func TestPagingReceiptPersistsAndClears(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &session{}
	s.observePaging(true, 0)
	if err := finishTruncation(&core.FetchData{Dir: dir, Receipt: s.receipt}); err != nil {
		t.Fatal(err)
	}
	receipt := readcommon.BundleTruncation(dir)
	if receipt == nil || receipt.TruncationReason != "scan_cap" || receipt.Returned != 0 {
		t.Fatalf("persisted paging receipt = %+v", receipt)
	}
	if err := finishTruncation(&core.FetchData{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if receipt := readcommon.BundleTruncation(dir); receipt != nil {
		t.Fatalf("stale receipt retained: %+v", receipt)
	}
}

func TestBitbucketPagingTruncationPreservesReturnedCount(t *testing.T) {
	t.Parallel()
	const base = "https://api.bitbucket.org/2.0/repos/o/r/pullrequests/7"
	dir := t.TempDir()
	s, _ := newTestSession(&scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(base + "/diffstat?pagelen=100"): {stdout: `{"values":[{"new":{"path":"a.go"}},{"new":{"path":"b.go"}}]}`},
		bitbucketCall(base + "/comments?pagelen=100"): {stdout: `{"values":[{"id":1,"content":{"raw":"first"},"user":{"display_name":"ana"}}],"next":"` + base + `/comments?cursor=2"}`},
		bitbucketCall(base + "/comments?cursor=2"):    {stderr: "curl: (22) 500", fail: true},
	}})
	if _, ref := bitbucketThreads(context.Background(), s, dir, bitbucketBearer(t), base, hostPR, "head111"); ref != nil {
		t.Fatalf("bitbucketThreads: %v", ref)
	}
	data := &core.FetchData{Dir: dir, Files: 2, Receipt: s.receipt}
	if err := finishTruncation(data); err != nil {
		t.Fatal(err)
	}
	if data.Receipt == nil || data.Receipt.TruncationReason != "scan_cap" || data.Receipt.Returned != 1 {
		t.Fatalf("bitbucket degraded threads receipt = %+v, want scan_cap returned=1 (1 thread fetched before page 2 failed)", data.Receipt)
	}
}

func TestForgejoFilePagingPreservesReturnedCount(t *testing.T) {
	t.Parallel()
	const fgCapBase = "https://example.invalid/api/v1/repos/o/r/pulls/8"
	fgOut := map[string]scriptResult{}
	for page := 1; page <= providerMaxPages; page++ {
		fgOut[forgejoCall(fmt.Sprintf("%s/files?limit=%d&page=%d", fgCapBase, forgejoPageSize, page))] = scriptResult{
			stdout: jsonRows(forgejoPageSize, func(int) string { return `{"id":1}` }),
		}
	}
	fgCap, _ := newTestSession(&scriptCommander{t: t, out: fgOut})
	_ = forgejoFiles(context.Background(), fgCap, forgejoCred(t), fgCapBase)
	if fgCap.receipt == nil || fgCap.receipt.Returned != providerMaxPages*forgejoPageSize {
		t.Fatalf("forgejo file-list paging receipt = %+v, want returned=%d", fgCap.receipt, providerMaxPages*forgejoPageSize)
	}
}

func TestChunkedFetchSetsEnvelopeChunkCapReceipt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+aa\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-b\n+bb\n"
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	chunkJSON, err := maybeWriteChunks(io.Discard, dir, 60, true, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", nil)
	if err != nil {
		t.Fatalf("maybeWriteChunks: %v", err)
	}
	data := &core.FetchData{Dir: dir, Files: 2}
	addFetchExtras(data, &budgetState{}, &fetchCap{}, chunkJSON)
	if ferr := finishTruncation(data); ferr != nil {
		t.Fatal(ferr)
	}
	if data.Receipt == nil || !data.Receipt.Truncated || data.Receipt.TruncationReason != "chunk_cap" ||
		data.Receipt.Returned != 2 || data.Receipt.Total == nil || *data.Receipt.Total != 2 {
		t.Fatalf("chunked fetch receipt = %+v, want chunk_cap returned=2 total=2", data.Receipt)
	}
	if readcommon.BundleTruncation(dir) != nil {
		t.Fatalf("chunked fetch should not write bundle truncation.json: %+v", readcommon.BundleTruncation(dir))
	}
}
