package sqliteread

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixtures were written by Python's sqlite3 module at a 1024-byte page:
// rollback.db holds opencode's credential schema with 120 small rows (an
// interior root), one row whose value overflows across pages, and one added
// after an ALTER TABLE ADD COLUMN, beside a table of every value width;
// wal.db holds a first commit checkpointed into the file and a second (an
// update and an insert) only in wal.db-wal.

func open(t *testing.T, name string, withWAL bool) *DB {
	t.Helper()
	file := readFixture(t, name)
	var wal []byte
	if withWAL {
		wal = readFixture(t, name+"-wal")
	}
	return openBytes(t, file, wal)
}

func openBytes(t *testing.T, file, wal []byte) *DB {
	t.Helper()
	db, err := Open(bytes.NewReader(file), int64(len(file)), bytes.NewReader(wal), int64(len(wal)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// byID indexes rows by their id column.
func byID(t *testing.T, rows []map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, r := range rows {
		id, ok := r["id"].(string)
		if !ok {
			t.Fatalf("row without a text id: %v", r)
		}
		out[id] = r
	}
	return out
}

func TestTableReadsEveryRowAcrossInteriorAndOverflowPages(t *testing.T) {
	rows, err := open(t, "rollback.db", false).Table("credential")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 122 {
		t.Fatalf("got %d rows, want 122", len(rows))
	}
	got := byID(t, rows)
	assertSmallRows(t, got)
	if v := got["big"]["value"]; v != `{"type":"key","key":"`+strings.Repeat("x", 3000)+`"}` {
		t.Errorf("overflowed value read as %d bytes", len(fmt.Sprint(v)))
	}
	if got["big"]["active"] != nil {
		t.Errorf("NULL active read as %v", got["big"]["active"])
	}
	if got["new"]["note"] != "added" || got["new"]["active"] != int64(0) {
		t.Errorf("row after ALTER TABLE = %v", got["new"])
	}
}

// assertSmallRows checks the 120 small rows the fixture wrote in a loop.
func assertSmallRows(t *testing.T, got map[string]map[string]any) {
	t.Helper()
	for i := range 120 {
		id := fmt.Sprintf("id-%03d", i)
		want := map[string]any{
			"id": id, "integration_id": fmt.Sprintf("int-%03d", i), "label": "label",
			"value": fmt.Sprintf(`{"type":"key","key":"k%03d"}`, i), "active": int64(1),
			"time_updated": int64(1000 + i), "note": nil,
		}
		if !mapsEqual(got[id], want) {
			t.Errorf("row %s = %v, want %v", id, got[id], want)
		}
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if got, ok := a[k]; !ok || fmt.Sprintf("%T %v", got, got) != fmt.Sprintf("%T %v", v, v) {
			return false
		}
	}
	return true
}

func TestTableDecodesEveryValueWidth(t *testing.T) {
	rows, err := open(t, "rollback.db", false).Table("other")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		int64(0), int64(1), int64(-1), int64(127), int64(-129), int64(32767), int64(8388607),
		int64(-2147483649), int64(140737488355327), int64(-9223372036854775808), 1.5,
		[]byte{0, 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if got := rows[i]["a"]; fmt.Sprintf("%T %v", got, got) != fmt.Sprintf("%T %v", w, w) {
			t.Errorf("row %d a = %T %v, want %T %v", i, got, got, w, w)
		}
	}
}

func TestTableReadsTheLogsLastCommit(t *testing.T) {
	cases := []struct {
		want    map[string]string
		name    string
		withWAL bool
	}{
		{name: "file alone", want: map[string]string{"a": `{"type":"key","key":"old"}`}},
		{name: "with the log", withWAL: true, want: map[string]string{
			"a": `{"type":"key","key":"new"}`, "b": `{"type":"key","key":"ds"}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := open(t, "wal.db", tc.withWAL).Table("credential")
			if err != nil {
				t.Fatal(err)
			}
			assertValues(t, rows, tc.want)
		})
	}
}

func assertValues(t *testing.T, rows []map[string]any, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for id, r := range byID(t, rows) {
		got[id] = fmt.Sprint(r["value"])
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("values = %v, want %v", got, want)
	}
}

// TestTableIgnoresAnIncompleteOrCorruptCommit proves only a whole, valid
// commit in the log counts: a torn last frame or a flipped byte in it drops
// that commit (the file's state is read), and trailing garbage after it is
// ignored.
func TestTableIgnoresAnIncompleteOrCorruptCommit(t *testing.T) {
	file, wal := readFixture(t, "wal.db"), readFixture(t, "wal.db-wal")
	frame := walFrameHead + 1024
	if (len(wal)-walHeaderSize)%frame != 0 {
		t.Fatalf("fixture log is not whole frames: %d bytes", len(wal))
	}
	old := map[string]string{"a": `{"type":"key","key":"old"}`}
	flipped := slices.Clone(wal)
	flipped[len(flipped)-1] ^= 0xff
	cases := []struct {
		want map[string]string
		name string
		wal  []byte
	}{
		{name: "torn last frame", wal: wal[:len(wal)-10], want: old},
		{name: "flipped byte", wal: flipped, want: old},
		{name: "trailing garbage", wal: append(slices.Clone(wal), bytes.Repeat([]byte{0xab}, frame)...), want: map[string]string{
			"a": `{"type":"key","key":"new"}`, "b": `{"type":"key","key":"ds"}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := openBytes(t, file, tc.wal).Table("credential")
			if err != nil {
				t.Fatal(err)
			}
			assertValues(t, rows, tc.want)
		})
	}
}

func TestTableNamesAMissingTable(t *testing.T) {
	if _, err := open(t, "rollback.db", false).Table("nope"); !errors.Is(err, ErrNoTable) {
		t.Fatalf("err = %v, want ErrNoTable", err)
	}
}

func TestOpenRefusesAFileThatIsNotADatabase(t *testing.T) {
	good := readFixture(t, "rollback.db")
	badSize := slices.Clone(good)
	binary.BigEndian.PutUint16(badSize[16:], 1000)
	utf16 := slices.Clone(good)
	binary.BigEndian.PutUint32(utf16[56:], 2)
	for name, raw := range map[string][]byte{
		"text":      []byte(strings.Repeat("not a database ", 10)),
		"page size": badSize,
		"utf16":     utf16,
		"short":     good[:50],
	} {
		if _, err := Open(bytes.NewReader(raw), int64(len(raw)), nil, 0); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

// TestTableRefusesAPageCycle proves a b-tree whose interior page points back
// at itself is an error, not an endless walk.
func TestTableRefusesAPageCycle(t *testing.T) {
	raw := slices.Clone(readFixture(t, "rollback.db"))
	root := credentialRoot(t, raw)
	at := (int(root) - 1) * 1024
	if raw[at] != pageInteriorTable {
		t.Fatalf("credential root page %d is not interior (kind %#x)", root, raw[at])
	}
	binary.BigEndian.PutUint32(raw[at+8:], root)
	db, err := Open(bytes.NewReader(raw), int64(len(raw)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Table("credential"); err == nil {
		t.Fatal("a self-referencing interior page read as a table")
	}
}

// credentialRoot reads the credential table's root page from the schema.
func credentialRoot(t *testing.T, raw []byte) uint32 {
	t.Helper()
	db, err := Open(bytes.NewReader(raw), int64(len(raw)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := db.records(1)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(schema, func(row []any) bool { return row[1] == "credential" })
	if i < 0 {
		t.Fatal("no credential table in the fixture")
	}
	root, ok := schema[i][3].(int64)
	if !ok {
		t.Fatalf("credential root page is %T", schema[i][3])
	}
	return uint32(root)
}

func TestColumnsNamesEachColumnAndSkipsConstraints(t *testing.T) {
	cases := map[string][]string{
		"CREATE TABLE `credential` (\n `id` text PRIMARY KEY,\n `value` text NOT NULL,\n" +
			" CONSTRAINT `pk` PRIMARY KEY(`id`, `value`)\n)": {"id", "value"},
		`CREATE TABLE t ("a b" int, [c] text CHECK (c IN ('x,y')), 'd''e', f DEFAULT (1), UNIQUE(f))`: {
			"a b", "c", "d'e", "f",
		},
		"CREATE TABLE t(x)": {"x"},
	}
	for sql, want := range cases {
		got, err := Columns(sql)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Columns(%q) = %q, %v; want %q", sql, got, err, want)
		}
	}
	for _, bad := range []string{"CREATE TABLE t", "CREATE TABLE t (a, 'b", "CREATE TABLE t (a, b"} {
		if _, err := Columns(bad); err == nil {
			t.Errorf("Columns(%q) succeeded", bad)
		}
	}
}

// FuzzTable feeds arbitrary bytes as the database file and as its log: any
// input is a table or an error, never a panic or a hang.
func FuzzTable(f *testing.F) {
	rollback, file, wal := fuzzSeed(f, "rollback.db"), fuzzSeed(f, "wal.db"), fuzzSeed(f, "wal.db-wal")
	f.Add(rollback, []byte(nil))
	f.Add(file, []byte(nil))
	f.Add(file, wal)
	f.Fuzz(func(t *testing.T, file, wal []byte) {
		db, err := Open(bytes.NewReader(file), int64(len(file)), bytes.NewReader(wal), int64(len(wal)))
		if err != nil {
			return
		}
		if rows, err := db.Table("credential"); err == nil && rows == nil {
			t.Fatal("Table returned neither rows nor an error")
		}
	})
}

// fuzzSeed reads one fixture for the fuzz corpus.
func fuzzSeed(f *testing.F, name string) []byte {
	f.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		f.Fatal(err)
	}
	return raw
}

// craftPage is the page size of the crafted files: 64 KiB, the largest, whose
// header spells it 1.
const craftPage = 1 << 16

// craftHeader writes a database header for a file of pages pages.
func craftHeader(file []byte, pages int) {
	copy(file, headerMagic)
	binary.BigEndian.PutUint16(file[offPageSize:], pageSizeAsMax)
	binary.BigEndian.PutUint32(file[offPageCount:], uint32(pages))
	binary.BigEndian.PutUint32(file[offChangeCount:], 1)
	binary.BigEndian.PutUint32(file[offValidFor:], 1)
	binary.BigEndian.PutUint32(file[offTextEncoding:], textUTF8)
}

// putVarint writes SQLite's varint for v (under 2^56) at b and returns its
// length.
func putVarint(b []byte, v uint64) int {
	var groups []byte
	for {
		groups = append([]byte{byte(v & 0x7f)}, groups...)
		if v >>= 7; v == 0 {
			break
		}
	}
	for i := range len(groups) - 1 {
		groups[i] |= 0x80
	}
	return copy(b, groups)
}

// craftCell writes at off one leaf cell holding a one-text-column record of
// size payload bytes whose overflow continues on page overflow, and returns
// its end.
func craftCell(page []byte, off, size int, overflow uint32) {
	at := off + putVarint(page[off:], uint64(size))
	at += putVarint(page[at:], 1)
	record := make([]byte, 0, 8)
	head := make([]byte, 8)
	n := putVarint(head, uint64(serialText+serialPerByte*(size-4)))
	record = append(append(record, byte(1+n)), head[:n]...)
	copy(page[at:], record)
	local := (&DB{usable: craftPage}).localSize(size)
	binary.BigEndian.PutUint32(page[at+local:], overflow)
}

// craftLeaf writes a leaf b-tree header at hdr pointing at cells.
func craftLeaf(page []byte, hdr int, cells []int) {
	page[hdr] = pageLeafTable
	binary.BigEndian.PutUint16(page[hdr+offCellCount:], uint16(len(cells)))
	for i, off := range cells {
		binary.BigEndian.PutUint16(page[hdr+leafHeaderSize+cellPointerSize*i:], uint16(off))
	}
}

// TestTableRefusesCellsSharingOnePayload proves a schema page whose 100
// pointers all name one 512 KiB cell, its overflow looping on one page, is
// refused before its payload is decoded a hundred times.
func TestTableRefusesCellsSharingOnePayload(t *testing.T) {
	t.Parallel()
	file := make([]byte, 2*craftPage)
	craftHeader(file, 2)
	const cell = 4096
	craftCell(file, cell, 512<<10, 2)
	craftLeaf(file, headerSize, slices.Repeat([]int{cell}, 100))
	binary.BigEndian.PutUint32(file[craftPage:], 2)
	db, err := Open(bytes.NewReader(file), int64(len(file)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Table("credential"); err == nil || errors.Is(err, ErrNoTable) {
		t.Fatalf("Table = %v, want the shared cell refused", err)
	}
}

// TestTableCapsTheBytesItDecodes proves distinct cells on many leaves, each
// a 1000000-byte record sharing one looping overflow page, stop at the
// decode cap instead of decoding every one.
func TestTableCapsTheBytesItDecodes(t *testing.T) {
	t.Parallel()
	const leaves = 20
	file := make([]byte, (leaves+2)*craftPage)
	craftHeader(file, leaves+2)
	file[headerSize] = pageInteriorTable
	binary.BigEndian.PutUint16(file[headerSize+offCellCount:], leaves-1)
	binary.BigEndian.PutUint32(file[headerSize+offRightChild:], leaves+2)
	for i := range leaves - 1 {
		off := 4096 + 8*i
		binary.BigEndian.PutUint16(file[headerSize+interiorHeadSize+cellPointerSize*i:], uint16(off))
		binary.BigEndian.PutUint32(file[off:], uint32(3+i))
		putVarint(file[off+childPointerSize:], uint64(i))
	}
	binary.BigEndian.PutUint32(file[craftPage:], 2)
	for leaf := range leaves {
		page := file[(2+leaf)*craftPage : (3+leaf)*craftPage]
		craftCell(page, 4096, 1000000, 2)
		craftLeaf(page, 0, []int{4096})
	}
	db, err := Open(bytes.NewReader(file), int64(len(file)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Table("credential"); err == nil || !strings.Contains(err.Error(), "decode cap") {
		t.Fatalf("Table = %v, want the decode cap", err)
	}
}

// TestColumnsRefusesAVirtualColumn proves a generated VIRTUAL column, which
// no record stores, refuses rather than shifting the columns after it; a
// STORED one is read.
func TestColumnsRefusesAVirtualColumn(t *testing.T) {
	t.Parallel()
	if _, err := Columns("CREATE TABLE t (a, b AS (a + 1), c)"); err == nil {
		t.Error("a VIRTUAL column was read")
	}
	if got, err := Columns("CREATE TABLE t (a, b INT GENERATED ALWAYS AS (a + 1) STORED, c)"); err != nil ||
		!slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("STORED = %q, %v", got, err)
	}
}

// TestTableReadsAFreshWALDatabase proves a database never checkpointed —
// its main-file header still leaves the encoding unset, page 1 living only in
// the log, as a fresh opencode database is — reads from its log, and the same
// main file without the log refuses.
func TestTableReadsAFreshWALDatabase(t *testing.T) {
	t.Parallel()
	file := readFixture(t, "fresh.db")
	if binary.BigEndian.Uint32(file[offTextEncoding:]) != 0 {
		t.Fatal("the fixture's main header names an encoding; it no longer covers the case")
	}
	rows, err := open(t, "fresh.db", true).Table("credential")
	if err != nil {
		t.Fatal(err)
	}
	assertValues(t, rows, map[string]string{"a": `{"type":"key","key":"fresh"}`})
	if _, err := Open(bytes.NewReader(file), int64(len(file)), nil, 0); err == nil {
		t.Error("the main file alone, encoding unset, opened")
	}
}

// TestOpenRefusesALogPastTheFrameCap proves a log whose frames run past the
// cap is an error, not its last commit before the cap, while a log that ends
// at the cap reads.
func TestOpenRefusesALogPastTheFrameCap(t *testing.T) {
	t.Parallel()
	file, wal := readFixture(t, "wal.db"), readFixture(t, "wal.db-wal")
	frames := int64((len(wal) - walHeaderSize) / (walFrameHead + 1024))
	for _, tc := range []struct {
		limit int64
		ok    bool
	}{{limit: frames, ok: true}, {limit: frames - 1}} {
		d, err := parseHeader(file, int64(len(file)))
		if err != nil {
			t.Fatal(err)
		}
		d.file = bytes.NewReader(file)
		err = d.indexWAL(bytes.NewReader(wal), int64(len(wal)), tc.limit)
		if (err == nil) != tc.ok {
			t.Errorf("a %d-frame log under a cap of %d = %v", frames, tc.limit, err)
		}
	}
}

// TestColumnsSkipsSQLComments proves a comment, which SQLite keeps in the
// schema's CREATE statement, is no column name, while the same text quoted
// is part of a name.
func TestColumnsSkipsSQLComments(t *testing.T) {
	t.Parallel()
	for sql, want := range map[string][]string{
		"CREATE TABLE credential (id TEXT, /* provider */ integration_id TEXT)":       {"id", "integration_id"},
		"CREATE TABLE t ( -- the key\n a TEXT, -- trailing, (with a paren\n b INT\n)": {"a", "b"},
		"CREATE TABLE t (\"a /* x */\" TEXT, `b--c` INT /* unclosed":                  nil,
		"CREATE TABLE t (\"a /* x */\" TEXT, `b--c` INT)":                             {"a /* x */", "b--c"},
		"CREATE TABLE t (a TEXT /* ) , evil */, b TEXT)":                              {"a", "b"},
	} {
		assertColumns(t, sql, want)
	}
}

// assertColumns checks one statement's columns, nil wanting an error.
func assertColumns(t *testing.T, sql string, want []string) {
	t.Helper()
	got, err := Columns(sql)
	switch {
	case want == nil && err == nil:
		t.Errorf("Columns(%q) = %q, want an error", sql, got)
	case want != nil && (err != nil || !slices.Equal(got, want)):
		t.Errorf("Columns(%q) = %q, %v; want %q", sql, got, err, want)
	}
}
