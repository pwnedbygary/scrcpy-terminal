package main

import (
	"strings"
	"unicode"
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
