package main

// ---------------------------------------------------------------------------
// on-screen software keyboard: bottom-anchored overlay with button-style
// keys (colored backgrounds = visual boundaries), staggered rows like a
// phone keyboard, and a top action bar. Mouse clicks press keys directly
// (hit zones = the rendered button). Toggle with Ctrl-K / Ctrl-O.
// ---------------------------------------------------------------------------

type kbKeyType int

const (
	kbText     kbKeyType = iota // emit this Android keycode (meta from modifiers)
	kbModifier                  // toggle a modifier (Shift/Ctrl/Alt/Meta)
	kbFunc                      // F1..F12 mapped through mapAppFKey
	kbAction                    // special action (grab, rotate, shade...)
)

type kbKey struct {
	label string
	key   uint32
	kind  kbKeyType
	wide  int // extra width in cells beyond the standard width (spacebar etc.)
}

type kbModifierState struct {
	shift, ctrl, alt, meta bool
}

func (m kbModifierState) androidMeta() uint32 {
	var v uint32
	if m.shift {
		v |= metaShiftOn
	}
	if m.ctrl {
		v |= metaCtrlOn
	}
	if m.alt {
		v |= metaAltOn
	}
	if m.meta {
		v |= 0x10000 // AMETA_META_ON
	}
	return v
}

// standard key button width in cells (label + padding)
const kbPad = 2

type kbCell struct {
	key   kbKey
	col   int // 0-based column where the button starts (in overlay line space)
	width int
}

type kbRow struct {
	cells []kbCell
}

type keyboard struct {
	open   bool
	rows   []kbRow
	curRow int
	curCol int
	mods   kbModifierState

	// press flash
	flashRow, flashCol int
}

const kbRowSpace = 1 // blank row between keyboard and the status bar

func newKeyboard() *keyboard {
	k := &keyboard{}
	k.rebuild()
	return k
}

func keyTextWidth(key kbKey) int {
	w := len(key.label) + kbPad
	if w < 4 {
		w = 4
	}
	return w + key.wide
}

func (k *keyboard) rebuild() {
	grid := kbLayout()
	k.rows = k.rows[:0]
	for _, row := range grid {
		var m kbRow
		col := 0
		for _, key := range row {
			w := keyTextWidth(key)
			m.cells = append(m.cells, kbCell{key: key, col: col, width: w})
			col += w + 2 // +2 gap between buttons
		}
		k.rows = append(k.rows, m)
	}
	k.curRow = len(k.rows) - 1
	k.curCol = 0
}

// kbLayout: bottom-to-top rows. Looks like a phone/tablet software keyboard:
// bottom = space+modifiers, then 3 letter rows (staggered), digits, then a
// nav row, function row, and the top action bar.
func kbLayout() [][]kbKey {
	return [][]kbKey{
		{ // bottom: space + modifier cluster
			{label: "SPACE", key: 62, kind: kbText, wide: 8},
			{label: "SHIFT", key: metaShiftOn, kind: kbModifier},
			{label: "CTRL", key: metaCtrlOn, kind: kbModifier},
			{label: "ALT", key: metaAltOn, kind: kbModifier},
			{label: "TAB", key: 61, kind: kbText},
			{label: "BCK", key: 67, kind: kbText},
			{label: "ENT", key: 66, kind: kbText},
		},
		{ // letter row 3 (bottom letters)
			{label: "Z", key: 54, kind: kbText},
			{label: "X", key: 52, kind: kbText},
			{label: "C", key: 31, kind: kbText},
			{label: "V", key: 50, kind: kbText},
			{label: "B", key: 30, kind: kbText},
			{label: "N", key: 42, kind: kbText},
			{label: "M", key: 46, kind: kbText},
			{label: ",", key: 55, kind: kbText},
			{label: ".", key: 56, kind: kbText},
			{label: "/", key: 76, kind: kbText},
		},
		{ // letter row 2 (home row)
			{label: "A", key: 29, kind: kbText},
			{label: "S", key: 47, kind: kbText},
			{label: "D", key: 32, kind: kbText},
			{label: "F", key: 34, kind: kbText},
			{label: "G", key: 35, kind: kbText},
			{label: "H", key: 36, kind: kbText},
			{label: "J", key: 38, kind: kbText},
			{label: "K", key: 40, kind: kbText},
			{label: "L", key: 41, kind: kbText},
			{label: ";", key: 74, kind: kbText},
			{label: "'", key: 75, kind: kbText},
		},
		{ // letter row 1 (top letters)
			{label: "Q", key: 45, kind: kbText},
			{label: "W", key: 51, kind: kbText},
			{label: "E", key: 33, kind: kbText},
			{label: "R", key: 46, kind: kbText},
			{label: "T", key: 48, kind: kbText},
			{label: "Y", key: 53, kind: kbText},
			{label: "U", key: 49, kind: kbText},
			{label: "I", key: 37, kind: kbText},
			{label: "O", key: 43, kind: kbText},
			{label: "P", key: 44, kind: kbText},
			{label: "[", key: 71, kind: kbText},
			{label: "]", key: 72, kind: kbText},
		},
		{ // digits
			{label: "1", key: 8, kind: kbText}, {label: "2", key: 9, kind: kbText},
			{label: "3", key: 10, kind: kbText}, {label: "4", key: 11, kind: kbText},
			{label: "5", key: 12, kind: kbText}, {label: "6", key: 13, kind: kbText},
			{label: "7", key: 14, kind: kbText}, {label: "8", key: 15, kind: kbText},
			{label: "9", key: 16, kind: kbText}, {label: "0", key: 7, kind: kbText},
			{label: "-", key: 69, kind: kbText}, {label: "=", key: 70, kind: kbText},
			{label: "\\", key: 73, kind: kbText},
		},
		{ // nav row: dpad + android system keys
			{label: "HOME", key: 3, kind: kbText},
			{label: "BACK", key: 4, kind: kbText},
			{label: "MENU", key: 82, kind: kbText},
			{label: "REC", key: 187, kind: kbText},
			{label: "PWR", key: 26, kind: kbText},
			{label: "UP", key: 19, kind: kbText},
			{label: "DN", key: 20, kind: kbText},
			{label: "LT", key: 21, kind: kbText},
			{label: "RT", key: 22, kind: kbText},
			{label: "OK", key: 23, kind: kbText},
			{label: "VOL+", key: 24, kind: kbText},
			{label: "VOL-", key: 25, kind: kbText},
			{label: "MUTE", key: 91, kind: kbText},
		},
		{ // function keys F1..F12
			{label: "F1", key: 131, kind: kbFunc}, {label: "F2", key: 132, kind: kbFunc},
			{label: "F3", key: 133, kind: kbFunc}, {label: "F4", key: 134, kind: kbFunc},
			{label: "F5", key: 135, kind: kbFunc}, {label: "F6", key: 136, kind: kbFunc},
			{label: "F7", key: 137, kind: kbFunc}, {label: "F8", key: 138, kind: kbFunc},
			{label: "F9", key: 139, kind: kbFunc}, {label: "F10", key: 140, kind: kbFunc},
			{label: "F11", key: 141, kind: kbFunc}, {label: "F12", key: 142, kind: kbFunc},
		},
		{ // top action bar
			{label: "ROTATE", key: 1, kind: kbAction},
			{label: "NOTIF", key: 2, kind: kbAction},
			{label: "SHADE", key: 3, kind: kbAction},
			{label: "COLLAPSE", key: 4, kind: kbAction},
			{label: "GRAB", key: 5, kind: kbAction},
			{label: "HIDE", key: 6, kind: kbAction},
			{label: "QUIT", key: 7, kind: kbAction},
		},
	}
}

// ---------------------------------------------------------------------------
// rendering: button-style keys. Each button is a colored pill span in the
// overlay line; gaps between buttons are transparent. The cursor key is
// reverse-video, the flash key is green.
// ---------------------------------------------------------------------------

func (k *keyboard) rowHeight() int { return len(k.rows) }

// lines returns overlay lines for the whole terminal (termRows tall),
// anchored at the bottom. Only the keyboard rows carry buttons.
func (k *keyboard) lines(termRows int) []overlayLine {
	n := k.rowHeight()
	kbTop := termRows - n - kbRowSpace - 2 // -2: status bar + spacer
	if kbTop < 0 {
		kbTop = 0
	}
	out := make([]overlayLine, termRows)
	for i := range out {
		out[i] = overlayLine{hlFrom: -1}
	}
	for ri := n - 1; ri >= 0; ri-- {
		ov := (n - 1 - ri) + kbTop
		if ov < 0 || ov >= termRows {
			continue
		}
		out[ov] = k.renderRow(ri)
	}
	return out
}

// button colors
const (
	btnReset    = "\x1b[0m"
	btnBg       = "\x1b[48;2;52;52;60m\x1b[38;2;235;235;245m"  // dark gray pill
	btnBgActive = "\x1b[48;2;90;90;130m\x1b[38;2;255;255;255m" // active modifier
	btnBgFlash  = "\x1b[48;2;0;120;60m\x1b[38;2;255;255;255m"  // pressed flash
)

type kbSeg = overlaySeg

func (k *keyboard) renderRow(ri int) overlayLine {
	row := k.rows[ri]
	var b []byte
	var segs []kbSeg
	for ci := range row.cells {
		c := row.cells[ci]
		label := keyLabel(c.key, k.mods)
		start := len(b)
		// button area: pad + centered label + pad
		pad := (c.width - len([]rune(label))) / 2
		if pad < 0 {
			pad = 0
		}
		for i := 0; i < pad; i++ {
			b = append(b, ' ')
		}
		b = append(b, []byte(label)...)
		for i := pad + len([]rune(label)); i < c.width; i++ {
			b = append(b, ' ')
		}
		// button background segment
		code := btnBg
		if ci == k.flashCol && ri == k.flashRow {
			code = btnBgFlash
		} else if c.key.kind == kbModifier && modifierActive(c.key.key, k.mods) {
			code = btnBgActive
		}
		segs = append(segs, kbSeg{from: start, to: len(b), code: code})
		// gap between buttons: 2 transparent spaces
		b = append(b, ' ', ' ')
	}
	return overlayLine{text: string(b), segs: segs}
}

func modifierActive(key uint32, mods kbModifierState) bool {
	switch key {
	case metaShiftOn:
		return mods.shift
	case metaCtrlOn:
		return mods.ctrl
	case metaAltOn:
		return mods.alt
	case 0x10000:
		return mods.meta
	}
	return false
}

func keyLabel(key kbKey, mods kbModifierState) string {
	if key.kind == kbModifier && modifierActive(key.key, mods) {
		return "[" + key.label + "]"
	}
	return key.label
}

// ---------------------------------------------------------------------------
// hit-testing: terminal cell (1-based) -> key, using the same geometry as
// render (button spans; transparent gaps do NOT hit). The cursor key is
// reverse-video for keyboard navigation.
// ---------------------------------------------------------------------------

func (k *keyboard) hitTestKeyCell(cellX, cellY, termRows int) (gridCell, bool) {
	n := k.rowHeight()
	kbTop := termRows - n - kbRowSpace - 2
	if kbTop < 0 {
		kbTop = 0
	}
	ov := cellY - 1
	rel := ov - kbTop
	if rel < 0 || rel >= n {
		return gridCell{}, false
	}
	layoutRow := n - 1 - rel
	row := k.rows[layoutRow]
	for ci, c := range row.cells {
		// button occupies [col, col+width); the 2-cell gap is a miss
		if cellX-1 >= c.col && cellX-1 < c.col+c.width {
			return gridCell{row: layoutRow, col: ci}, true
		}
	}
	return gridCell{}, false
}

type gridCell struct {
	row, col int
}

func (k *keyboard) hitTest(cellX, cellY, termRows int) (kbKey, bool) {
	if c, ok := k.hitTestKeyCell(cellX, cellY, termRows); ok {
		return k.rows[c.row].cells[c.col].key, true
	}
	return kbKey{}, false
}

// ---------------------------------------------------------------------------
// actions
// ---------------------------------------------------------------------------

func (k *keyboard) act(a *app, key kbKey) {
	k.flash(-1, -1) // clear previous flash
	switch key.kind {
	case kbText:
		if a.ctrl != nil {
			a.sendAndroidKeyMeta(key.key, k.mods.androidMeta())
		}
		// one-shot modifiers, like a real keyboard
		k.mods = kbModifierState{}
	case kbModifier:
		switch key.key {
		case metaShiftOn:
			k.mods.shift = !k.mods.shift
		case metaCtrlOn:
			k.mods.ctrl = !k.mods.ctrl
		case metaAltOn:
			k.mods.alt = !k.mods.alt
		case 0x10000:
			k.mods.meta = !k.mods.meta
		}
	case kbFunc:
		a.mapAppFKey(key.key)
	case kbAction:
		switch key.key {
		case 1:
			if a.ctrl != nil {
				a.ctrl.rotate()
			}
		case 2:
			if a.ctrl != nil {
				a.ctrl.expandNotifications()
			}
		case 3:
			if a.ctrl != nil {
				a.ctrl.expandSettings()
			}
		case 4:
			if a.ctrl != nil {
				a.ctrl.collapsePanels()
			}
		case 5:
			a.toggleGrab()
			a.refreshKeyboard()
		case 6:
			a.closeKeyboard()
		case 7:
			a.events <- inputEvent{kind: evQuit}
		}
	}
}

func (k *keyboard) navigate(dx, dy int) {
	// clear press flash on navigation (it is only a momentary confirm)
	k.flashRow, k.flashCol = -1, -1
	n := len(k.rows)
	if n == 0 {
		return
	}
	k.curRow += dy
	k.curCol += dx
	if k.curRow < 0 {
		k.curRow = n - 1
	}
	if k.curRow >= n {
		k.curRow = 0
	}
	row := k.rows[k.curRow]
	if len(row.cells) == 0 {
		k.curCol = 0
		return
	}
	if k.curCol < 0 {
		k.curCol = len(row.cells) - 1
	}
	if k.curCol >= len(row.cells) {
		k.curCol = 0
	}
}

func (k *keyboard) currentKey() (kbKey, bool) {
	if k.curRow < 0 || k.curRow >= len(k.rows) {
		return kbKey{}, false
	}
	row := k.rows[k.curRow]
	if k.curCol < 0 || k.curCol >= len(row.cells) {
		return kbKey{}, false
	}
	return row.cells[k.curCol].key, true
}

// setFlashCurrent marks the cursor cell as flashed (for Enter presses).
func (k *keyboard) setFlashCurrent() {
	k.flash(k.curRow, k.curCol)
}

// flash marks a key as just-pressed (visual confirmation).
func (k *keyboard) flash(row, col int) {
	k.flashRow = row
	k.flashCol = col
}
