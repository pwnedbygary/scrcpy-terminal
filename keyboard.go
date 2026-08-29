package main

// ---------------------------------------------------------------------------
// on-screen keyboard: a bottom-anchored grid of clickable keys rendered as an
// overlay over the video. Toggle with Ctrl-O. Mouse clicks hit-test keys;
// arrows+Enter navigate; modifiers are sticky (persist until another key).
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

type kbCell struct {
	key   kbKey
	col   int
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

	// press flash: which key flashed last, in (row,col) of the grid
	flashRow, flashCol int
}

const kbRowSpace = 1 // blank row between kb top and video

func newKeyboard() *keyboard {
	k := &keyboard{}
	k.rebuild()
	return k
}

func keyTextWidth(key kbKey) int {
	w := len(key.label) + 2
	if w < 3 {
		w = 3
	}
	return w
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
			col += w + 1 // +1 gap
		}
		k.rows = append(k.rows, m)
	}
	k.curRow = len(k.rows) - 1
	k.curCol = 0
}

// kbLayout returns rows bottom-to-top: rows[0] is the bottom row (SPACE),
// rows[len-1] is the top row (system actions).
func kbLayout() [][]kbKey {
	return [][]kbKey{
		{ // bottom: space + modifiers
			{label: "SPACE", key: 62, kind: kbText},
			{label: "SHIFT", key: metaShiftOn, kind: kbModifier},
			{label: "CTRL", key: metaCtrlOn, kind: kbModifier},
			{label: "ALT", key: metaAltOn, kind: kbModifier},
			{label: "DL", key: 67, kind: kbText},  // DEL / backspace
			{label: "ENT", key: 66, kind: kbText}, // enter
			{label: "TAB", key: 61, kind: kbText},
			{label: "ESC", key: 111, kind: kbText},
		},
		{ // qwerty home row
			{label: "A", key: 29, kind: kbText}, {label: "S", key: 47, kind: kbText},
			{label: "D", key: 32, kind: kbText}, {label: "F", key: 34, kind: kbText},
			{label: "G", key: 35, kind: kbText}, {label: "H", key: 36, kind: kbText},
			{label: "J", key: 38, kind: kbText}, {label: "K", key: 40, kind: kbText},
			{label: "L", key: 41, kind: kbText}, {label: ";", key: 74, kind: kbText},
			{label: "'", key: 75, kind: kbText}, {label: "\\", key: 73, kind: kbText},
		},
		{ // qwerty top row
			{label: "Q", key: 45, kind: kbText}, {label: "W", key: 51, kind: kbText},
			{label: "E", key: 33, kind: kbText}, {label: "R", key: 46, kind: kbText},
			{label: "T", key: 48, kind: kbText}, {label: "Y", key: 53, kind: kbText},
			{label: "U", key: 49, kind: kbText}, {label: "I", key: 37, kind: kbText},
			{label: "O", key: 43, kind: kbText}, {label: "P", key: 44, kind: kbText},
			{label: "[", key: 71, kind: kbText}, {label: "]", key: 72, kind: kbText},
		},
		{ // digits
			{label: "1", key: 8, kind: kbText}, {label: "2", key: 9, kind: kbText},
			{label: "3", key: 10, kind: kbText}, {label: "4", key: 11, kind: kbText},
			{label: "5", key: 12, kind: kbText}, {label: "6", key: 13, kind: kbText},
			{label: "7", key: 14, kind: kbText}, {label: "8", key: 15, kind: kbText},
			{label: "9", key: 16, kind: kbText}, {label: "0", key: 7, kind: kbText},
			{label: "-", key: 69, kind: kbText}, {label: "=", key: 70, kind: kbText},
		},
		{ // nav: d-pad + android keys
			{label: "Home", key: 3, kind: kbText},
			{label: "Back", key: 4, kind: kbText},
			{label: "Menu", key: 82, kind: kbText},
			{label: "Rec", key: 187, kind: kbText},
			{label: "Pwr", key: 26, kind: kbText},
			{label: "Up", key: 19, kind: kbText},
			{label: "Dn", key: 20, kind: kbText},
			{label: "Lt", key: 21, kind: kbText},
			{label: "Rt", key: 22, kind: kbText},
			{label: "OK", key: 23, kind: kbText},
			{label: "VolU", key: 24, kind: kbText},
			{label: "VolD", key: 25, kind: kbText},
			{label: "Mute", key: 91, kind: kbText},
		},
		{ // function keys F1..F12
			{label: "F1", key: 131, kind: kbFunc}, {label: "F2", key: 132, kind: kbFunc},
			{label: "F3", key: 133, kind: kbFunc}, {label: "F4", key: 134, kind: kbFunc},
			{label: "F5", key: 135, kind: kbFunc}, {label: "F6", key: 136, kind: kbFunc},
			{label: "F7", key: 137, kind: kbFunc}, {label: "F8", key: 138, kind: kbFunc},
			{label: "F9", key: 139, kind: kbFunc}, {label: "F10", key: 140, kind: kbFunc},
			{label: "F11", key: 141, kind: kbFunc}, {label: "F12", key: 142, kind: kbFunc},
		},
		{ // top: system actions
			{label: "Rotate", key: 1, kind: kbAction},
			{label: "Notif", key: 2, kind: kbAction},
			{label: "Shade", key: 3, kind: kbAction},
			{label: "Collap", key: 4, kind: kbAction},
			{label: "Grab", key: 5, kind: kbAction},
			{label: "Hide", key: 6, kind: kbAction}, // close keyboard
			{label: "Quit", key: 7, kind: kbAction}, // quit sct (Alt+Q equivalent)
		},
	}
}

// ---------------------------------------------------------------------------
// rendering: returns overlay lines (row 0 = top of screen), with the kb
// anchored at the bottom. The cursor key gets a highlight segment.
// ---------------------------------------------------------------------------

func (k *keyboard) lines(termRows int) []overlayLine {
	n := len(k.rows)
	kbTop := termRows - n - kbRowSpace - 2 // -2: one for status bar, one spacer
	if kbTop < 0 {
		kbTop = 0
	}
	out := make([]overlayLine, termRows)
	for i := range out {
		out[i] = overlayLine{hlFrom: -1, flFrom: -1, flTo: -1}
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

func (k *keyboard) renderRow(ri int) overlayLine {
	row := k.rows[ri]
	line := overlayLine{hlFrom: -1, flFrom: -1}
	var b []byte
	for ci := range row.cells {
		c := row.cells[ci]
		label := keyLabel(c.key, k.mods)
		start := len(b)
		if ci == k.curCol && ri == k.curRow {
			line.hlFrom = start
			line.hlTo = start + c.width
		}
		if ci == k.flashCol && ri == k.flashRow {
			line.flFrom = start
			line.flTo = start + c.width
		}
		// center the label inside the key width
		for i := 0; i < c.width; i++ {
			b = append(b, ' ')
		}
		copy(b[start+(c.width-len(label))/2:], label)
	}
	line.text = string(b)
	return line
}

// flash marks the given grid cell as just-pressed (visual confirmation).
func (k *keyboard) flash(row, col int) {
	k.flashRow = row
	k.flashCol = col
}

func keyLabel(key kbKey, mods kbModifierState) string {
	if key.kind == kbModifier {
		active := false
		switch key.key {
		case metaShiftOn:
			active = mods.shift
		case metaCtrlOn:
			active = mods.ctrl
		case metaAltOn:
			active = mods.alt
		case 0x10000:
			active = mods.meta
		}
		if active {
			return "[" + key.label + "]"
		}
	}
	return key.label
}

// ---------------------------------------------------------------------------
// hit-testing: terminal cell (1-based) -> key
// ---------------------------------------------------------------------------

// hitTestKeyCell returns the grid cell (row index in kb layout, col index)
// under the given terminal cell. Used for the press flash.
func (k *keyboard) hitTestKeyCell(cellX, cellY, termRows int) (gridCell, bool) {
	n := len(k.rows)
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
	n := len(k.rows)
	kbTop := termRows - n - kbRowSpace - 2
	if kbTop < 0 {
		kbTop = 0
	}
	ov := cellY - 1
	rel := ov - kbTop
	if rel < 0 || rel >= n {
		return kbKey{}, false
	}
	row := k.rows[n-1-rel]
	for _, c := range row.cells {
		if cellX-1 >= c.col && cellX-1 < c.col+c.width {
			return c.key, true
		}
	}
	return kbKey{}, false
}

// ---------------------------------------------------------------------------
// actions
// ---------------------------------------------------------------------------

// act performs the key action and records a visual flash for the pressed
// key (the overlay re-renders with that key highlighted until the next
// frame draw; keep the flash brief by clearing it on the next act).
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

// setFlashCurrent marks the cursor cell as flashed (for Enter presses).
func (k *keyboard) setFlashCurrent() {
	k.flash(k.curRow, k.curCol)
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
