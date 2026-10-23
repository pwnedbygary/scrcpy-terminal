package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// display cells: how much of a terminal row a string takes
//
// A rune is not a cell. CJK characters and most emoji take two cells and
// combining marks none, so a row cut or padded by runes can run past the edge
// of the screen; on the status row, the last line, that scrolls the whole
// picture up. Device names and messages come from outside and can hold any of
// these, and control characters as well.
// ---------------------------------------------------------------------------

// wideRunes are the ranges terminals draw two cells wide: Unicode's East Asian
// Wide and Fullwidth characters, with the emoji blocks taken whole. Counting a
// narrow character as wide only leaves a cell unused; the other way round
// overflows the row. Sorted.
var wideRunes = [...][2]rune{
	{0x1100, 0x115F}, // Hangul Jamo initial consonants
	// emoji in the BMP that are drawn as pictures
	{0x231A, 0x231B}, {0x2329, 0x232A}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0},
	{0x23F3, 0x23F3}, {0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653},
	{0x267F, 0x267F}, {0x2693, 0x2693}, {0x26A1, 0x26A1}, {0x26AA, 0x26AB},
	{0x26BD, 0x26BE}, {0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4},
	{0x26EA, 0x26EA}, {0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA},
	{0x26FD, 0x26FD}, {0x2705, 0x2705}, {0x270A, 0x270B}, {0x2728, 0x2728},
	{0x274C, 0x274C}, {0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757},
	{0x2795, 0x2797}, {0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C},
	{0x2B50, 0x2B50}, {0x2B55, 0x2B55},
	// CJK, Yi, Hangul and the fullwidth forms
	{0x2E80, 0x303E}, {0x3041, 0x33FF}, {0x3400, 0x4DBF}, {0x4E00, 0x9FFF},
	{0xA000, 0xA4CF}, {0xA960, 0xA97F}, {0xAC00, 0xD7A3}, {0xF900, 0xFAFF},
	{0xFE10, 0xFE19}, {0xFE30, 0xFE6F}, {0xFF00, 0xFF60}, {0xFFE0, 0xFFE6},
	{0x16FE0, 0x16FE4}, {0x17000, 0x18CFF}, {0x1B000, 0x1B2FF},
	// emoji and pictographs outside the BMP
	{0x1F004, 0x1F004}, {0x1F0CF, 0x1F0CF}, {0x1F18E, 0x1F18E}, {0x1F191, 0x1F19A},
	{0x1F200, 0x1F2FF}, {0x1F300, 0x1F64F}, {0x1F680, 0x1F6FF}, {0x1F7E0, 0x1F7EB},
	{0x1F900, 0x1F9FF}, {0x1FA70, 0x1FAFF},
	{0x20000, 0x3FFFD}, // CJK extension B and later
}

// runeCells is the number of cells r takes.
func runeCells(r rune) int {
	switch {
	case r < 0x20 || r >= 0x7f && r < 0xa0:
		return 0 // control characters (cleanText takes them out of what is shown)
	case r < 0x300:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	}
	for _, w := range wideRunes {
		if r < w[0] {
			break
		}
		if r <= w[1] {
			return 2
		}
	}
	return 1
}

// cellWidth is the number of cells s takes.
func cellWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeCells(r)
	}
	return n
}

// textCells is how long text from outside is, for the limits that keep it
// short: its cells, a character that takes none counting as one, so that no
// run of combining marks can make it longer without end.
func textCells(s string) int {
	n := 0
	for _, r := range s {
		n += max(runeCells(r), 1)
	}
	return n
}

// truncateCells cuts s to at most n cells, never inside a character.
func truncateCells(s string, n int) string {
	used := 0
	for i, r := range s {
		w := runeCells(r)
		if used+w > n {
			return s[:i]
		}
		used += w
	}
	return s
}

// ellipsize fits s into n cells, ending in "…" when it had to be cut.
func ellipsize(s string, n int) string {
	if cellWidth(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	return truncateCells(s, n-1) + "…"
}

// cleanText makes text from outside (a device name, an error) safe to show on
// one row: control and format characters, which can move the cursor, recolour
// the row or reorder it, and line breaks become single spaces.
func cleanText(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			gap = b.Len() > 0
			continue
		}
		if gap {
			b.WriteByte(' ')
			gap = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// shownAsIs reports whether r shows on a terminal as what it is: printable,
// and not blank-looking, as U+2800 (a Braille cell without dots) and the
// Hangul fillers are, which can pad a row or pass for a space.
func shownAsIs(r rune) bool {
	return unicode.IsPrint(r) && r != '\u2800' && !unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
}

// quoteText shows text from outside (a device's name) as data: in double
// quotes, with an escape, as Go writes one, for each character that does not
// show as what it is (control, format and blank-looking characters, spaces
// other than the ASCII one, bytes that are not UTF-8), so that it can neither
// act on the terminal nor pad a row, and a backslash before each ", \, $,
// backquote and !, so that copied with its quotes into sh, bash, zsh or fish
// it substitutes nothing, and an interactive bash or zsh expands no history
// in it (there !# brings back the line so far, opening quote and all, and the
// rest of the name runs). Only an interactive zsh takes out the backslash
// before a !; the others keep it, and fish the one before a backquote too. It
// is cut to n cells between the quotes, as textCells counts them, and then
// ends in "…" after them.
func quoteText(s string, n int) string {
	var b strings.Builder
	b.WriteByte('"')
	used := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		var shown string
		switch {
		case r == utf8.RuneError && size == 1:
			shown = fmt.Sprintf(`\x%02x`, s[0])
		case r == '"' || r == '\\' || r == '$' || r == '`' || r == '!':
			shown = `\` + string(r)
		case shownAsIs(r):
			shown = string(r)
		default:
			q := strconv.QuoteRuneToASCII(r)
			shown = q[1 : len(q)-1]
		}
		if used += textCells(shown); used > n {
			b.WriteString(`"…`)
			return b.String()
		}
		b.WriteString(shown)
		s = s[size:]
	}
	b.WriteByte('"')
	return b.String()
}
