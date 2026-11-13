package fetchcore

import (
	"context"
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
	s.observePaging(true)
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
