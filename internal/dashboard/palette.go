package dashboard

import "github.com/stainless-code/verdict/internal/core"

// role is what a cell means, not how it looks: the palette resolves it to an
// SGR sequence at emit time, so a frame carries no escape bytes.
type role uint8

// The semantic roles a view may paint with.
const (
	rolePlain role = iota
	roleTitle
	roleMuted
	roleBorder
	roleTab
	roleOK
	roleWarn
	roleBad
	roleCount
)

// The two sequences both palettes share.
const (
	sgrReset = "\x1b[0m"
	sgrBold  = "\x1b[0;1m"
	sgrTab   = "\x1b[0;1;7m"
)

// palette maps every role to the SGR sequence that selects it. Each sequence
// starts from a reset, so emitting one never inherits the previous style.
type palette [roleCount]string

// paletteANSI uses the 16 ANSI colors only: the one palette the user's own
// terminal theme already chose, so it reads on any background.
var paletteANSI = palette{
	rolePlain:  sgrReset,
	roleTitle:  sgrBold,
	roleMuted:  "\x1b[0;90m",
	roleBorder: "\x1b[0;90m",
	roleTab:    sgrTab,
	roleOK:     "\x1b[0;32m",
	roleWarn:   "\x1b[0;33m",
	roleBad:    "\x1b[0;1;31m",
}

// paletteMono carries attributes only, for NO_COLOR and --color never. Every
// state also has a text shape (a `!` marker, a `—` for an unknown count), so
// nothing depends on the attribute either.
var paletteMono = palette{
	rolePlain:  sgrReset,
	roleTitle:  sgrBold,
	roleMuted:  "\x1b[0;2m",
	roleBorder: "\x1b[0;2m",
	roleTab:    sgrTab,
	roleOK:     sgrReset,
	roleWarn:   sgrBold,
	roleBad:    "\x1b[0;1;4m",
}

// charset is the handful of non-ASCII glyphs a frame draws.
type charset struct {
	rule     rune // horizontal rule
	ellipsis rune // a truncated value's last cell
	unknown  rune // a count the layer could not trust
	sep      rune // between header fields
}

var (
	charsetUnicode = charset{rule: '─', ellipsis: '…', unknown: '—', sep: '·'}
	charsetASCII   = charset{rule: '-', ellipsis: '~', unknown: '-', sep: '|'}
)

// look is one resolved presentation: the palette and the glyphs.
type look struct {
	pal *palette
	cs  charset
}

// pickLook resolves the presentation once per session. --color never and
// NO_COLOR (already folded into color by the global flag layer) select the
// attribute-only palette; TERM=dumb also drops to ASCII glyphs.
func pickLook(color, term string) look {
	if term == "dumb" {
		return look{pal: &paletteMono, cs: charsetASCII}
	}
	if color == core.ColorNever {
		return look{pal: &paletteMono, cs: charsetUnicode}
	}
	return look{pal: &paletteANSI, cs: charsetUnicode}
}
