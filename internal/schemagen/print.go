package schemagen

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// printWidth is the bun/Prettier print width the published JSON uses.
const printWidth = 80

// controlLimit is the first non-printable ASCII code point.
const controlLimit = 0x20

// Hex nibble extraction for \u00XX escapes.
const (
	hexShift = 4
	hexMask  = 0xf
)

// printDocument renders one schema node as bun-stable JSON: two-space indent,
// LF line endings, one trailing newline.
func printDocument(root *node) []byte {
	var b strings.Builder
	renderNode(&b, root, 0, 0, 0)
	b.WriteByte('\n')
	return []byte(b.String())
}

// renderNode writes n at the given indentation and current column. tail is
// the number of columns that follow the value on the same line (a comma, or
// the parent's closing brace when the parent prints flat).
func renderNode(b *strings.Builder, n *node, base, col, tail int) {
	flat := flatString(n)
	if flattenable(n) && col+displayWidth(flat)+tail <= printWidth {
		b.WriteString(flat)
		return
	}
	switch n.kind {
	case kindObject:
		renderObject(b, n, base)
	case kindArray:
		renderArray(b, n, base)
	case kindString, kindNumber, kindBool, kindNull:
		b.WriteString(flat)
	}
}

// renderObject writes an object one member per line.
func renderObject(b *strings.Builder, n *node, base int) {
	b.WriteString("{\n")
	for i, m := range n.members {
		b.WriteString(indent(base + 2))
		prefix := quote(m.key) + ": "
		b.WriteString(prefix)
		nextTail := 0
		if i < len(n.members)-1 {
			nextTail = 1
		}
		renderNode(b, m.value, base+2, base+2+displayWidth(prefix), nextTail)
		writeSeparator(b, i, len(n.members))
	}
	b.WriteString(indent(base))
	b.WriteString("}")
}

// renderArray writes an array one element per line.
func renderArray(b *strings.Builder, n *node, base int) {
	b.WriteString("[\n")
	for i, elem := range n.elems {
		b.WriteString(indent(base + 2))
		nextTail := 0
		if i < len(n.elems)-1 {
			nextTail = 1
		}
		renderNode(b, elem, base+2, base+2, nextTail)
		writeSeparator(b, i, len(n.elems))
	}
	b.WriteString(indent(base))
	b.WriteString("]")
}

// writeSeparator writes the comma/newline between siblings.
func writeSeparator(b *strings.Builder, index, count int) {
	if index < count-1 {
		b.WriteString(",\n")
		return
	}
	b.WriteString("\n")
}

// flattenable reports whether n and all its descendants print on one line.
func flattenable(n *node) bool {
	switch n.kind {
	case kindObject:
		return !n.forceMulti && membersFlattenable(n.members)
	case kindArray:
		return elemsFlattenable(n.elems)
	case kindString, kindNumber, kindBool, kindNull:
		return true
	}
	return true
}

// membersFlattenable reports whether every member value can print flat.
func membersFlattenable(members []member) bool {
	for _, m := range members {
		if !flattenable(m.value) {
			return false
		}
	}
	return true
}

// elemsFlattenable reports whether every array element can print flat.
func elemsFlattenable(elems []*node) bool {
	for _, elem := range elems {
		if !flattenable(elem) {
			return false
		}
	}
	return true
}

// flatString renders the single-line form of n for fit measurement and for
// values that print flat.
func flatString(n *node) string {
	switch n.kind {
	case kindObject:
		return flatObject(n)
	case kindArray:
		return flatArray(n)
	case kindString:
		return quote(n.text)
	case kindNumber:
		return n.text
	case kindBool:
		return strconv.FormatBool(n.boolean)
	case kindNull:
		return typeNull
	}
	return typeNull
}

// flatObject renders an object's single-line form.
func flatObject(n *node) string {
	if len(n.members) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{ ")
	for i, m := range n.members {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(m.key))
		b.WriteString(": ")
		b.WriteString(flatString(m.value))
	}
	b.WriteString(" }")
	return b.String()
}

// flatArray renders an array's single-line form.
func flatArray(n *node) string {
	if len(n.elems) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, elem := range n.elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(flatString(elem))
	}
	b.WriteByte(']')
	return b.String()
}

// indent returns n spaces.
func indent(n int) string {
	return strings.Repeat(" ", n)
}

// displayWidth approximates Prettier's string width for the ASCII and
// typographic characters the schemas use.
func displayWidth(s string) int {
	return utf8.RuneCountInString(s)
}

// quote renders a JSON string with literal non-ASCII characters (Prettier
// keeps them) and no HTML escaping.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		if writeEscape(&b, r) {
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// writeEscape writes one escaped control or quote rune and reports whether
// the rune needed escaping.
func writeEscape(b *strings.Builder, r rune) bool {
	switch r {
	case '"':
		b.WriteString(`\"`)
	case '\\':
		b.WriteString(`\\`)
	case '\b':
		b.WriteString(`\b`)
	case '\f':
		b.WriteString(`\f`)
	case '\n':
		b.WriteString(`\n`)
	case '\r':
		b.WriteString(`\r`)
	case '\t':
		b.WriteString(`\t`)
	default:
		return writeControlEscape(b, r)
	}
	return true
}

// writeControlEscape writes the \u00XX form for other control characters.
func writeControlEscape(b *strings.Builder, r rune) bool {
	if r >= controlLimit {
		return false
	}
	const hex = "0123456789abcdef"
	b.WriteString(`\u00`)
	b.WriteByte(hex[r>>hexShift])
	b.WriteByte(hex[r&hexMask])
	return true
}
