// Package sqliteread reads the rows of one rowid table from a SQLite database
// file, read-only, without SQLite: verdict seeds an agent's throwaway home
// with the login a CLI keeps in its database (opencode's credential table),
// and the module takes no dependencies. It reads the main file and the
// committed frames of its write-ahead log as the format documents them
// (https://www.sqlite.org/fileformat2.html, read 2026-11-27), takes no lock,
// and writes nothing. Every page number, cell, payload, and overflow chain is
// bounds-checked, and the walk is capped, so a malformed or concurrently
// rewritten file is an error, never a panic or an unbounded read. Values
// come back as nil, int64, float64, string, or []byte; a column a row's
// record lacks (added by a later ALTER TABLE) reads as nil, and an INTEGER
// PRIMARY KEY column, which SQLite stores as the rowid, reads as nil too.
// It reads rowid tables only: a WITHOUT ROWID table is an error, and so is
// a table with a generated VIRTUAL column, which SQLite stores in no record.
package sqliteread

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// ErrNoTable is the error Table returns when the schema names no such
// rowid table.
var ErrNoTable = errors.New("no such table")

// The database header: its magic string and the offsets of the fields read.
const (
	headerMagic     = "SQLite format 3\x00"
	headerSize      = 100
	offPageSize     = 16
	offReserved     = 20
	offChangeCount  = 24
	offPageCount    = 28
	offTextEncoding = 56
	offValidFor     = 92
	textUTF8        = 1
	pageSizeMax     = 1 << 16
	// pageSizeAsMax is how the header spells pageSizeMax.
	pageSizeAsMax  = 1
	pageSizeMin    = 512
	usableMin      = 480
	firstPage      = 1
	schemaColumns  = 5
	schemaType     = 0
	schemaName     = 1
	schemaRootPage = 3
	schemaSQL      = 4
)

// The write-ahead log: its header, each frame's header, and their fields.
const (
	walHeaderSize = 32
	walFrameHead  = 24
	walMagicLE    = 0x377f0682
	walMagicBE    = 0x377f0683
	walVersion    = 3007000
	offWALVersion = 4
	offWALPage    = 8
	offWALSalt    = 16
	offWALSum     = 24
	offFrameSize  = 4
	offFrameSalt  = 8
	offFrameSum   = 16
	saltSize      = 8
	sumWord       = 4
	sumStep       = 8
)

// A b-tree page header and its cells.
const (
	pageLeafTable     = 0x0d
	pageInteriorTable = 0x05
	offCellCount      = 3
	offRightChild     = 8
	leafHeaderSize    = 8
	interiorHeadSize  = 12
	cellPointerSize   = 2
	childPointerSize  = 4
	overflowNextSize  = 4
	// The overflow rule's constants (fileformat2 §1.6, table b-tree leaf).
	overflowMaxLocal = 35
	overflowMinBase  = 12
	overflowMinScale = 32
	overflowMinDiv   = 255
	overflowMinLess  = 23
)

// Record serial types and varints.
const (
	serialNull    = 0
	serialInt64   = 6
	serialFloat   = 7
	serialZero    = 8
	serialOne     = 9
	serialBlob    = 12
	serialText    = 13
	serialPerByte = 2
	floatSize     = 8
	varintMax     = 9
	varintLow     = 0x7f
	varintMore    = 0x80
	varintBits    = 7
	byteBits      = 8
	int64Bits     = 64
)

// The caps on one Table call.
const (
	// maxDepth bounds the b-tree descent; a real table is a handful deep.
	maxDepth = 32
	// maxPages bounds the pages one Table call reads, overflow included.
	maxPages = 1 << 16
	// maxPayload bounds one record: a login row is small.
	maxPayload = 1 << 20
	// maxDecoded bounds the record bytes one Table call decodes, the schema
	// included, so pages whose cells share an overflow chain cannot grow it.
	maxDecoded = 16 << 20
	// maxWALFrames bounds the log scanned.
	maxWALFrames = 1 << 20
)

// DB is one opened database: the main file and its log's committed pages.
type DB struct {
	file     io.ReaderAt
	wal      io.ReaderAt
	frames   map[uint32]int64
	pageSize int
	usable   int
	reads    int
	decoded  int
	pages    uint32
}

// Open reads the database header from file (size bytes) and, when the log
// wal is not empty, indexes its committed frames (walSize bytes): the last
// valid commit decides each page and the database's page count.
func Open(file io.ReaderAt, size int64, wal io.ReaderAt, walSize int64) (*DB, error) {
	head := make([]byte, headerSize)
	if _, err := file.ReadAt(head, 0); err != nil {
		return nil, jq.NewErr("database header unreadable")
	}
	d, err := parseHeader(head, size)
	if err != nil {
		return nil, err
	}
	d.file = file
	if wal != nil && walSize > 0 {
		if err := d.indexWAL(wal, walSize, maxWALFrames); err != nil {
			return nil, err
		}
	}
	return d, d.checkEncoding()
}

// checkEncoding requires a UTF-8 database, read from page 1 as the log's
// last commit leaves it: a database in write-ahead-log mode keeps the
// encoding unset in its main file until a checkpoint copies page 1 back.
func (d *DB) checkEncoding() error {
	p, err := d.page(firstPage)
	d.reads = 0
	if err != nil {
		return err
	}
	if binary.BigEndian.Uint32(p[offTextEncoding:]) != textUTF8 {
		return jq.NewErr("not a UTF-8 database")
	}
	return nil
}

// parseHeader checks the database header and reads its geometry.
func parseHeader(head []byte, size int64) (*DB, error) {
	if string(head[:len(headerMagic)]) != headerMagic {
		return nil, jq.NewErr("not a SQLite database")
	}
	d, err := geometry(head)
	if err != nil {
		return nil, err
	}
	d.pages = uint32(min(max(size, 0)/int64(d.pageSize), math.MaxUint32)) //nolint:gosec // clamped to uint32 above
	if n := binary.BigEndian.Uint32(head[offPageCount:]); n != 0 &&
		binary.BigEndian.Uint32(head[offChangeCount:]) == binary.BigEndian.Uint32(head[offValidFor:]) {
		d.pages = min(d.pages, n)
	}
	return d, nil
}

// geometry reads the page size and its usable part from the header.
func geometry(head []byte) (*DB, error) {
	d := &DB{pageSize: int(binary.BigEndian.Uint16(head[offPageSize:]))}
	if d.pageSize == pageSizeAsMax {
		d.pageSize = pageSizeMax
	}
	if d.pageSize < pageSizeMin || d.pageSize&(d.pageSize-1) != 0 {
		return nil, jq.NewErr("invalid page size")
	}
	if d.usable = d.pageSize - int(head[offReserved]); d.usable < usableMin {
		return nil, jq.NewErr("invalid reserved space")
	}
	return d, nil
}

// walScan is the state of one pass over the log's frames.
type walScan struct {
	order   binary.ByteOrder
	pending map[uint32]int64
	salt    []byte
	s0, s1  uint32
}

// indexWAL maps each page to its frame in the log's last valid commit: a
// frame counts only while its salts match the header and its running
// checksum holds, and only up to the last commit frame; a log with no valid
// header is ignored, as SQLite ignores it.
func (d *DB) indexWAL(wal io.ReaderAt, walSize, limit int64) error {
	scan, ok, err := d.walHeader(wal, walSize)
	if err != nil || !ok {
		return err
	}
	d.wal, d.frames = wal, map[uint32]int64{}
	frame := make([]byte, walFrameHead+d.pageSize)
	for i := range limit {
		if more, err := d.nextFrame(scan, frame, walHeaderSize+i*int64(len(frame)), walSize); !more {
			return err
		}
	}
	// A log longer than the cap may commit past it: its last commit is
	// unknown, and an older one would read stale rows.
	if walHeaderSize+(limit+1)*int64(len(frame)) > walSize {
		return nil
	}
	return jq.NewErr("write-ahead log longer than the frame cap")
}

// nextFrame reads the frame at off and records it; false once the log's
// valid frames end there.
func (d *DB) nextFrame(scan *walScan, frame []byte, off, walSize int64) (bool, error) {
	if off+int64(len(frame)) > walSize {
		return false, nil
	}
	if _, err := d.wal.ReadAt(frame, off); err != nil {
		return false, jq.NewErr("write-ahead log frame unreadable")
	}
	if !scan.frame(frame) {
		return false, nil
	}
	d.commit(scan, frame, off)
	return true, nil
}

// commit records one valid frame, and when it ends a transaction makes the
// transaction's frames the pages read.
func (d *DB) commit(scan *walScan, frame []byte, off int64) {
	scan.pending[binary.BigEndian.Uint32(frame)] = off + walFrameHead
	if size := binary.BigEndian.Uint32(frame[offFrameSize:]); size != 0 {
		maps.Copy(d.frames, scan.pending)
		clear(scan.pending)
		d.pages = size
	}
}

// walHeader reads and checks the log header; false when the log is not
// one SQLite would use.
func (d *DB) walHeader(wal io.ReaderAt, walSize int64) (*walScan, bool, error) {
	if walSize < walHeaderSize {
		return nil, false, nil
	}
	head := make([]byte, walHeaderSize)
	if _, err := wal.ReadAt(head, 0); err != nil {
		return nil, false, jq.NewErr("write-ahead log header unreadable")
	}
	order, ok := walOrder(binary.BigEndian.Uint32(head))
	if !ok || binary.BigEndian.Uint32(head[offWALVersion:]) != walVersion {
		return nil, false, nil
	}
	if binary.BigEndian.Uint32(head[offWALPage:]) != uint32(d.pageSize) { //nolint:gosec // a page size is at most 64 KiB
		return nil, false, jq.NewErr("write-ahead log page size differs from the database's")
	}
	scan := &walScan{order: order, salt: head[offWALSalt : offWALSalt+saltSize], pending: map[uint32]int64{}}
	scan.s0, scan.s1 = walChecksum(order, head[:offWALSum], 0, 0)
	return scan, scan.matches(head[offWALSum:]), nil
}

// frame reports whether one frame belongs to the log and its checksum
// holds, carrying the running checksum forward.
func (s *walScan) frame(frame []byte) bool {
	if !bytes.Equal(frame[offFrameSalt:offFrameSalt+saltSize], s.salt) {
		return false
	}
	s.s0, s.s1 = walChecksum(s.order, frame[:offFrameSalt], s.s0, s.s1)
	s.s0, s.s1 = walChecksum(s.order, frame[walFrameHead:], s.s0, s.s1)
	return s.matches(frame[offFrameSum:])
}

// matches compares the running checksum with the two words at sum.
func (s *walScan) matches(sum []byte) bool {
	return s.s0 == binary.BigEndian.Uint32(sum) && s.s1 == binary.BigEndian.Uint32(sum[sumWord:])
}

// walOrder is the byte order a log's magic number names for its checksums.
func walOrder(magic uint32) (binary.ByteOrder, bool) {
	switch magic {
	case walMagicLE:
		return binary.LittleEndian, true
	case walMagicBE:
		return binary.BigEndian, true
	}
	return nil, false
}

// walChecksum continues the log's running checksum over b, whose length is
// a multiple of 8.
func walChecksum(order binary.ByteOrder, b []byte, s0, s1 uint32) (sum0, sum1 uint32) {
	for i := 0; i+sumStep <= len(b); i += sumStep {
		s0 += order.Uint32(b[i:]) + s1
		s1 += order.Uint32(b[i+sumWord:]) + s0
	}
	return s0, s1
}

// page reads one page, from the log when it holds the page.
func (d *DB) page(n uint32) ([]byte, error) {
	if n == 0 || n > d.pages {
		return nil, jq.NewErr("page number out of range")
	}
	if d.reads++; d.reads > maxPages {
		return nil, jq.NewErr("table larger than the read cap")
	}
	buf := make([]byte, d.pageSize)
	src, off := d.file, int64(n-1)*int64(d.pageSize)
	if at, ok := d.frames[n]; ok {
		src, off = d.wal, at
	}
	if _, err := src.ReadAt(buf, off); err != nil {
		return nil, jq.NewErr("page unreadable")
	}
	return buf, nil
}

// Table returns every row of the named rowid table, each a map from column
// name to value.
func (d *DB) Table(name string) ([]map[string]any, error) {
	d.reads, d.decoded = 0, 0
	schema, err := d.records(firstPage)
	if err != nil {
		return nil, err
	}
	for _, row := range schema {
		if len(row) >= schemaColumns && row[schemaType] == "table" && row[schemaName] == name {
			return d.tableRows(row)
		}
	}
	return nil, ErrNoTable //nolint:wrapcheck // the package's own documented sentinel
}

// tableRows reads one schema row's table.
func (d *DB) tableRows(row []any) ([]map[string]any, error) {
	root, ok := row[schemaRootPage].(int64)
	sql, isText := row[schemaSQL].(string)
	if !ok || !isText || root <= 0 || root > math.MaxUint32 {
		return nil, jq.NewErr("schema row malformed")
	}
	cols, err := Columns(sql)
	if err != nil {
		return nil, err
	}
	recs, err := d.records(uint32(root))
	if err != nil {
		return nil, err
	}
	return named(cols, recs), nil
}

// named maps each record's values onto the column names.
func named(cols []string, recs [][]any) []map[string]any {
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = nil
			if i < len(rec) {
				row[c] = rec[i]
			}
		}
		out = append(out, row)
	}
	return out
}

// walk is one b-tree walk: the pages it has entered and the records it has
// decoded.
type walk struct {
	d    *DB
	seen map[uint32]bool
	out  [][]any
}

// records walks one table b-tree from its root and decodes every leaf
// record.
func (d *DB) records(root uint32) ([][]any, error) {
	w := &walk{d: d, seen: map[uint32]bool{}}
	err := w.visit(root, 0)
	return w.out, err
}

// visit walks one page: its children first, in key order, then its cells.
func (w *walk) visit(n uint32, depth int) error {
	if depth > maxDepth || w.seen[n] {
		return jq.NewErr("b-tree cycle or depth past the cap")
	}
	w.seen[n] = true
	p, err := w.d.page(n)
	if err != nil {
		return err
	}
	children, cells, err := w.d.cells(p, headerOffset(n))
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := w.visit(c, depth+1); err != nil {
			return err
		}
	}
	return w.leaves(p, cells)
}

// headerOffset is where page n's b-tree header starts: past the database
// header on the first page.
func headerOffset(n uint32) int {
	if n == firstPage {
		return headerSize
	}
	return 0
}

// leaves decodes one leaf page's cells, once their extents on the page are
// proved disjoint: two pointers to one cell, or into each other's, would
// decode its payload again for every pointer.
func (w *walk) leaves(p []byte, cells []int) error {
	if err := w.d.disjoint(p, cells); err != nil {
		return err
	}
	for _, off := range cells {
		rec, err := w.d.leafCell(p, off)
		if err != nil {
			return err
		}
		w.out = append(w.out, rec)
	}
	return nil
}

// disjoint refuses leaf cells whose bytes on the page overlap.
func (d *DB) disjoint(p []byte, cells []int) error {
	extents := make([][2]int, 0, len(cells))
	for _, off := range cells {
		end, err := d.cellEnd(p, off)
		if err != nil {
			return err
		}
		extents = append(extents, [2]int{off, end})
	}
	slices.SortFunc(extents, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })
	for i := 1; i < len(extents); i++ {
		if extents[i][0] < extents[i-1][1] {
			return jq.NewErr("leaf cells overlap on their page")
		}
	}
	return nil
}

// cellEnd is where one leaf cell's bytes on the page end: its two varints,
// its local payload, and its overflow pointer when it has one.
func (d *DB) cellEnd(p []byte, off int) (int, error) {
	size, n := varint(p[off:d.usable])
	if n == 0 || size > maxPayload {
		return 0, jq.NewErr("cell payload size malformed")
	}
	end := off + n
	if _, n = varint(p[end:d.usable]); n == 0 {
		return 0, jq.NewErr("cell rowid malformed")
	}
	local := d.localSize(int(size))
	end += n + local
	if local < int(size) {
		end += overflowNextSize
	}
	if end > d.usable {
		return 0, jq.NewErr("cell past the page")
	}
	return end, nil
}

// cells reads one table page's header: an interior page's children in key
// order (the right-most last), or a leaf page's cell offsets.
func (d *DB) cells(p []byte, hdr int) (children []uint32, leaf []int, err error) {
	if hdr+interiorHeadSize > len(p) {
		return nil, nil, jq.NewErr("page header truncated")
	}
	kind, count := p[hdr], int(binary.BigEndian.Uint16(p[hdr+offCellCount:]))
	var ptrs int
	switch kind {
	case pageLeafTable:
		ptrs = hdr + leafHeaderSize
	case pageInteriorTable:
		ptrs = hdr + interiorHeadSize
	default:
		return nil, nil, jq.NewErr("not a rowid table page")
	}
	offs, err := d.cellOffsets(p, ptrs, count)
	if err != nil || kind == pageLeafTable {
		return nil, offs, err
	}
	for _, off := range offs {
		if off+childPointerSize > d.usable {
			return nil, nil, jq.NewErr("interior cell truncated")
		}
		children = append(children, binary.BigEndian.Uint32(p[off:]))
	}
	return append(children, binary.BigEndian.Uint32(p[hdr+offRightChild:])), nil, nil
}

// cellOffsets reads a page's cell pointer array, each pointing past it and
// inside the usable page.
func (d *DB) cellOffsets(p []byte, ptrs, count int) ([]int, error) {
	end := ptrs + cellPointerSize*count
	if end > d.usable {
		return nil, jq.NewErr("cell pointer array past the page")
	}
	offs := make([]int, 0, count)
	for i := range count {
		off := int(binary.BigEndian.Uint16(p[ptrs+cellPointerSize*i:]))
		if off < end || off >= d.usable {
			return nil, jq.NewErr("cell offset out of range")
		}
		offs = append(offs, off)
	}
	return offs, nil
}

// leafCell decodes one leaf cell's record, following its overflow chain.
func (d *DB) leafCell(p []byte, off int) ([]any, error) {
	size, n := varint(p[off:d.usable])
	if n == 0 || size > maxPayload {
		return nil, jq.NewErr("cell payload size malformed")
	}
	off += n
	if _, n = varint(p[off:d.usable]); n == 0 {
		return nil, jq.NewErr("cell rowid malformed")
	}
	if d.decoded += int(size); d.decoded > maxDecoded {
		return nil, jq.NewErr("table larger than the decode cap")
	}
	payload, err := d.payload(p, off+n, int(size))
	if err != nil {
		return nil, err
	}
	return record(payload)
}

// payload gathers a cell's size payload bytes: those on the page, then its
// overflow pages.
func (d *DB) payload(p []byte, off, size int) ([]byte, error) {
	local := d.localSize(size)
	if off+local > d.usable {
		return nil, jq.NewErr("cell payload past the page")
	}
	out := append(make([]byte, 0, size), p[off:off+local]...)
	if local == size {
		return out, nil
	}
	if off+local+overflowNextSize > d.usable {
		return nil, jq.NewErr("overflow pointer past the page")
	}
	next := binary.BigEndian.Uint32(p[off+local:])
	for len(out) < size {
		o, err := d.page(next)
		if err != nil {
			return nil, err
		}
		take := min(size-len(out), d.usable-overflowNextSize)
		out = append(out, o[overflowNextSize:overflowNextSize+take]...)
		next = binary.BigEndian.Uint32(o)
	}
	return out, nil
}

// localSize is how much of a size-byte payload a table leaf keeps on its
// page, per the format's overflow rule.
func (d *DB) localSize(size int) int {
	x := d.usable - overflowMaxLocal
	if size <= x {
		return size
	}
	m := (d.usable-overflowMinBase)*overflowMinScale/overflowMinDiv - overflowMinLess
	if k := m + (size-m)%(d.usable-overflowNextSize); k <= x {
		return k
	}
	return m
}

// record decodes one record: its header of serial types, then the values.
func record(b []byte) ([]any, error) {
	types, body, err := recordHeader(b)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(types))
	for _, t := range types {
		v, size, err := value(t, body)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		body = body[size:]
	}
	return out, nil
}

// recordHeader splits a record into its serial types and its body.
func recordHeader(b []byte) (types []uint64, body []byte, err error) {
	hsize, n := varint(b)
	if n == 0 || hsize > uint64(len(b)) || hsize < uint64(n) { //nolint:gosec // lengths are never negative
		return nil, nil, jq.NewErr("record header malformed")
	}
	head := b[n:hsize]
	for len(head) > 0 {
		t, m := varint(head)
		if m == 0 {
			return nil, nil, jq.NewErr("record serial type malformed")
		}
		types = append(types, t)
		head = head[m:]
	}
	return types, b[hsize:], nil
}

// value decodes one value of serial type t from the front of b and returns
// how many bytes it took.
func value(t uint64, b []byte) (v any, size int, err error) {
	switch {
	case t == serialNull:
		return nil, 0, nil
	case t <= serialInt64:
		return intValue(t, b)
	case t == serialFloat:
		if len(b) < floatSize {
			return nil, 0, jq.NewErr("record value truncated")
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), floatSize, nil
	case t == serialZero, t == serialOne:
		return int64(t - serialZero), 0, nil
	case t < serialBlob:
		return nil, 0, jq.NewErr("reserved serial type")
	}
	return bytesValue(t, b)
}

// intSizes are the byte widths of serial types 1 through 6.
var intSizes = [...]int{1, 2, 3, 4, 6, 8}

// intValue decodes a big-endian two's-complement integer of serial type t.
func intValue(t uint64, b []byte) (v any, size int, err error) {
	size = intSizes[t-1]
	if size > len(b) {
		return nil, 0, jq.NewErr("record value truncated")
	}
	var n int64
	for _, c := range b[:size] {
		n = n<<byteBits | int64(c)
	}
	shift := int64Bits - byteBits*size
	return n << shift >> shift, size, nil
}

// bytesValue decodes a blob (even t) or text (odd t) value.
func bytesValue(t uint64, b []byte) (v any, size int, err error) {
	n := (t - serialBlob) / serialPerByte
	if n > uint64(len(b)) {
		return nil, 0, jq.NewErr("record value truncated")
	}
	size = int(n) //nolint:gosec // at most len(b)
	if t%serialPerByte == serialText%serialPerByte {
		return string(b[:size]), size, nil
	}
	return append([]byte(nil), b[:size]...), size, nil
}

// varint decodes SQLite's big-endian varint (up to 9 bytes, the ninth all
// 8 bits) and returns its length, 0 when b ends first.
func varint(b []byte) (v uint64, size int) {
	for i := range min(len(b), varintMax) {
		if i == varintMax-1 {
			return v<<byteBits | uint64(b[i]), varintMax
		}
		v = v<<varintBits | uint64(b[i]&varintLow)
		if b[i] < varintMore {
			return v, i + 1
		}
	}
	return 0, 0
}

// tableConstraints are the words that open a table constraint, not a
// column, in a CREATE TABLE list.
var tableConstraints = []string{"CONSTRAINT", "PRIMARY", "UNIQUE", "CHECK", "FOREIGN"}

// Columns names a CREATE TABLE statement's columns in order, unquoting
// "x", `x`, [x], and 'x' names.
func Columns(sql string) ([]string, error) {
	_, list, ok := strings.Cut(stripComments(sql), "(")
	if !ok {
		return nil, jq.NewErr("CREATE TABLE has no column list")
	}
	defs, err := splitDefs(list)
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, def := range defs {
		name, quoted := firstToken(strings.TrimSpace(def))
		switch {
		case name == "":
			return nil, jq.NewErr("column definition malformed")
		case virtualColumn(def):
			return nil, jq.NewErr("a generated VIRTUAL column is not stored in the record")
		case quoted || !containsFold(tableConstraints, name):
			cols = append(cols, name)
		}
	}
	return cols, nil
}

// virtualColumn reports a generated column SQLite does not store: one with
// an AS (expression) clause that is not STORED.
func virtualColumn(def string) bool {
	upper := strings.Join(strings.Fields(strings.ToUpper(def)), " ")
	generated := strings.Contains(upper, " AS (") || strings.Contains(upper, " AS(")
	return generated && !strings.Contains(upper, " STORED")
}

// stripComments replaces each SQL comment — `--` to the end of its line,
// `/* … */` — with a space, leaving quoted text as it is; SQLite keeps a
// CREATE statement's comments in its schema.
func stripComments(sql string) string {
	var b strings.Builder
	for i := 0; i < len(sql); i++ {
		switch {
		case strings.ContainsRune("'\"`[", rune(sql[i])):
			end := closingQuote(sql, i)
			if end < 0 {
				end = len(sql) - 1
			}
			b.WriteString(sql[i : end+1])
			i = end
		case strings.HasPrefix(sql[i:], "--"):
			i = commentEnd(sql, i, "\n", 0)
			b.WriteByte(' ')
		case strings.HasPrefix(sql[i:], "/*"):
			i = commentEnd(sql, i, "*/", len("*/")-1)
			b.WriteByte(' ')
		default:
			b.WriteByte(sql[i])
		}
	}
	return b.String()
}

// commentEnd is the index of a comment's last character: the closer after
// it (and tail more), or the end of sql for an unclosed one.
func commentEnd(sql string, i int, closer string, tail int) int {
	if end := strings.Index(sql[i+2:], closer); end >= 0 {
		return i + 2 + end + tail
	}
	return len(sql) - 1
}

// splitter walks a column list for its top-level commas.
type splitter struct {
	s     string
	defs  []string
	depth int
	start int
}

// splitDefs splits a column list at its top-level commas, up to the
// parenthesis that closes it, skipping quoted text.
func splitDefs(s string) ([]string, error) {
	sp := &splitter{s: s}
	for i := 0; i < len(s); i++ {
		next, done, err := sp.step(i)
		if err != nil || done {
			return sp.defs, err
		}
		i = next
	}
	return nil, jq.NewErr("unterminated column list in CREATE TABLE")
}

// step handles the character at i: the index to continue from, and whether
// the list closed there.
func (sp *splitter) step(i int) (next int, done bool, err error) {
	switch sp.s[i] {
	case '\'', '"', '`', '[':
		end := closingQuote(sp.s, i)
		if end < 0 {
			return 0, false, jq.NewErr("unterminated quote in CREATE TABLE")
		}
		return end, false, nil
	case '(':
		sp.depth++
	case ')':
		if sp.depth == 0 {
			sp.defs = append(sp.defs, sp.s[sp.start:i])
			return i, true, nil
		}
		sp.depth--
	case ',':
		if sp.depth == 0 {
			sp.defs = append(sp.defs, sp.s[sp.start:i])
			sp.start = i + 1
		}
	}
	return i, false, nil
}

// closingQuote is the index of the quote that closes the one at i, a
// doubled quote standing for itself; -1 when none does.
func closingQuote(s string, i int) int {
	closer := s[i]
	if closer == '[' {
		closer = ']'
	}
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] != closer:
		case closer != ']' && j+1 < len(s) && s[j+1] == closer:
			j++
		default:
			return j
		}
	}
	return -1
}

// firstToken is a definition's first word, unquoted, and whether it was
// quoted.
func firstToken(def string) (token string, quoted bool) {
	if def == "" {
		return "", false
	}
	if !strings.ContainsRune("'\"`[", rune(def[0])) {
		end := strings.IndexAny(def, " \t\r\n(")
		if end < 0 {
			end = len(def)
		}
		return def[:end], false
	}
	end := closingQuote(def, 0)
	if end < 0 {
		return "", false
	}
	inner := def[1:end]
	if q := def[0]; q != '[' {
		inner = strings.ReplaceAll(inner, string(q)+string(q), string(q))
	}
	return inner, true
}

// containsFold reports whether word is one of words, ignoring case.
func containsFold(words []string, word string) bool {
	for _, w := range words {
		if strings.EqualFold(w, word) {
			return true
		}
	}
	return false
}
