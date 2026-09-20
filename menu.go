package main

// ---------------------------------------------------------------------------
// action menu: the TUI's discoverable control list
//
// The terminal has no buttons to click, so the equivalent of the browser's
// action bar is a small key-driven menu: press Alt+/ and every device action
// is listed with the chord that runs it. It is deliberately the same action
// set and the same letters as the web bar, so muscle memory carries between
// the terminal, a browser tab and a phone -- see appMnemonicOps in appkeys.go.
//
// The menu is an overlay in the same sense as the software keyboard: while it
// is open it owns the keyboard, and clicks on a row run that row's action.
// ---------------------------------------------------------------------------

// menuItem is one row: the chord letter, the op it runs, and the label shown.
type menuItem struct {
	Key   byte   // letter pressed to run it (0 = no chord)
	Op    string // app-level op, or one of the local pseudo-ops below
	Label string // what the row says
}

// Pseudo-ops with no device action of their own.
const (
	opKeyboard = "keyboard" // show/hide the software keyboard (browser: #ime focus)
	opMenuKey  = "menukey"  // the Android Menu key (82); Alt+O types an o instead
)

// menuItems is the action list, in display order. It is the TUI twin of the
// browser toolbar; keep the two in step (TestMenuMatchesTheChordTable pins the
// ops to appMnemonicOps, TestPlayerKeysAreTheTerminalKeys pins the browser).
var menuItems = []menuItem{
	{'h', "home", "Home"},
	{'b', "back", "Back"},
	{'t', "appswitch", "Recents"},
	{0, opMenuKey, "Menu key"},
	{'p', "power", "Power"},
	{'u', "volup", "Volume up"},
	{'d', "voldown", "Volume down"},
	{'c', "collapse", "Collapse panels"},
	{'n', "notif", "Notifications"},
	{'e', "settings", "Settings shade"},
	{'r', "rotate", "Rotate"},
	{'g', "grab", "Grab"},
	{'s', "screenshot", "Screenshot"},
	{'k', "resetvideo", "Keyframe"},
	{'i', opKeyboard, "Keyboard"},
	{'q', "quit", "Quit"},
}

// toggleMenu opens/closes the action overlay. Like the keyboard it takes a
// brief mouse grab so the click-to-run-a-row path works under Zellij, and it
// closes the software keyboard first: two overlays cannot share the screen.
func (a *app) toggleMenu() {
	if a.tui == nil {
		return
	}
	if a.menuOpen {
		a.closeMenu()
		return
	}
	if a.kb != nil && a.kb.open {
		a.closeKeyboard()
	}
	a.menuOpen = true
	if !a.grabbed {
		a.setMouse(true)
		a.kbAutoGrabbed = true
	}
	a.refreshMenu()
}

func (a *app) closeMenu() {
	if !a.menuOpen {
		return
	}
	a.menuOpen = false
	if a.kbAutoGrabbed && a.grabbed {
		a.setMouse(false)
	}
	a.kbAutoGrabbed = false
	if a.tui != nil {
		a.refreshOverlays()
	}
}

// menuAction runs one menu row. Device ops and quit go through the same
// applyAppOp the F-keys use; the local ops (screenshot, keyboard, the Menu
// keycode) are handled here.
func (a *app) menuAction(op string) {
	switch op {
	case opKeyboard:
		a.toggleKeyboard()
	case opMenuKey:
		a.sendAndroidKeyMeta(appKeyOps["menu"], 0)
	case "screenshot":
		a.screenshot()
	default:
		a.applyAppOp(op)
	}
}

// menuKeyInput handles keyboard input while the menu is open. It consumes the
// single-byte keys a menu can use (row letters, Esc, Enter, Ctrl-Q) and returns
// false for everything else -- escape sequences in particular, because those
// carry the mouse and arrow keys, which must still reach the normal input path.
func (a *app) menuKeyInput(b []byte) bool {
	if !a.menuOpen || len(b) != 1 {
		return false
	}
	switch b[0] {
	case 0x1b, 0x0d, 0x0a: // Esc/Enter: close
		a.closeMenu()
		return true
	case 0x11:
		// Ctrl-Q quits the app. Outside the menu Ctrl-Q goes to the device as
		// its own chord instead (input.go has no global Ctrl-Q: Zellij owns
		// it), but while this modal menu is up it is the natural way out.
		a.events <- inputEvent{kind: evQuit}
		return true
	}
	// A menu row letter runs it; any other printable key closes the menu and is
	// swallowed rather than typed, so a stray letter never reaches the device
	// through an overlay that is already gone.
	if it, ok := menuItemForKey(b[0]); ok {
		a.closeMenu()
		a.menuAction(it.Op)
		return true
	}
	a.closeMenu()
	return true
}

// menuItemForKey finds the row a letter selects (case-insensitive).
func menuItemForKey(c byte) (menuItem, bool) {
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	for _, it := range menuItems {
		if it.Key != 0 && it.Key == c {
			return it, true
		}
	}
	return menuItem{}, false
}

// menuHit is one clickable menu row: its 1-based terminal row and what it runs.
type menuHit struct {
	row  int
	item menuItem
}

// menuHitTest maps a terminal cell (1-based) to a menu row, using the layout
// refreshMenu recorded when the menu was painted.
func (a *app) menuHitTest(cellX, cellY, rows int) (menuItem, bool) {
	_ = cellX
	for _, h := range a.menuHits {
		if h.row == cellY {
			return h.item, true
		}
	}
	// No recorded layout (menu painted before a resize): fall back to the
	// header offset so a click still lands on the row under the cursor. The
	// status bar is rows-1, exactly as in refreshMenu.
	line := cellY - 1
	if line < menuHeader || line >= menuHeader+len(menuItems) || line >= rows-1 {
		return menuItem{}, false
	}
	return menuItems[line-menuHeader], true
}

// menuHeader is the number of overlay lines before the first row (title +
// blank). Shared by menuLines and menuHitTest.
const menuHeader = 2

// menuLeftMargin indents the whole box from the left edge of the terminal.
const menuLeftMargin = 2

// menuLines builds the overlay. Every line is padded to the same width so the
// row backgrounds form a box; the TUI painter then fills the rest of the line.
func (a *app) menuLines(rows int) []overlayLine {
	width := 0
	label := func(it menuItem) string {
		if it.Key == 0 {
			return "     " + it.Label
		}
		return string(it.Key) + " ·  " + it.Label
	}
	body := make([]string, 0, len(menuItems))
	for _, it := range menuItems {
		s := " " + label(it) + " "
		body = append(body, s)
		if len([]rune(s)) > width {
			width = len([]rune(s))
		}
	}
	title := " scterm · actions "
	if len([]rune(title)) > width {
		width = len([]rune(title))
	}
	footer := " Alt+/ toggle · Esc close · letters run "
	if len([]rune(footer)) > width {
		width = len([]rune(footer))
	}
	fit := func(s string) string {
		if pad := width - len([]rune(s)); pad > 0 {
			s += spaces(pad)
		}
		return s
	}
	t := fit(title)
	lines := []overlayLine{{text: t, segs: []overlaySeg{{0, len([]rune(t)), menuTitleStyle}}}}
	lines = append(lines, overlayLine{text: spaces(width), segs: []overlaySeg{{0, width, menuRowStyle}}})
	for _, s := range body {
		s = fit(s)
		lines = append(lines, overlayLine{text: s, segs: []overlaySeg{{0, len([]rune(s)), menuRowStyle}}})
	}
	f := fit(footer)
	lines = append(lines, overlayLine{text: f, segs: []overlaySeg{{0, len([]rune(f)), menuFootStyle}}})
	if rows > 0 && len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// refreshMenu records the screen rows the rows landed on for click
// hit-testing, then repaints. The painter itself lives in refreshOverlays, so
// the menu, the keyboard and the action bar can never fight for the overlay.
func (a *app) refreshMenu() {
	if a.tui == nil || !a.menuOpen {
		return
	}
	_, rows := termSize()

	// Screen row of each menu row: the overlay starts at the top of the video
	// area, one line per entry, after the header. menuHitTest consumes this, so
	// a click and the painter share one layout instead of two calculations.
	//
	// The bound is rows-1, not rows: the last terminal line is the status bar
	// (tui.resize: t.rows = rows-1), and an overlay line painted at rows would
	// land on it. Recording it would let a click on the status bar run an
	// invisible action -- at 18 rows that action is Quit.
	a.menuHits = a.menuHits[:0]
	for i := range menuItems {
		line := menuHeader + i // 0-based overlay/screen line
		if line >= rows-1 {
			break
		}
		a.menuHits = append(a.menuHits, menuHit{row: line + 1, item: menuItems[i]})
	}
	a.refreshOverlays()
}

// padLines prepends n spaces to every line (a left margin for the overlay).
func padLines(lines []overlayLine, n int) []overlayLine {
	if n <= 0 {
		return lines
	}
	pad := spaces(n)
	out := make([]overlayLine, len(lines))
	for i, l := range lines {
		shifted := l
		shifted.text = pad + l.text
		for j := range shifted.segs {
			shifted.segs[j].from += n
			shifted.segs[j].to += n
		}
		out[i] = shifted
	}
	return out
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// Menu styling: SGR spans in the same shape as the keyboard's button spans.
const (
	menuTitleStyle = "\x1b[48;2;40;40;48m\x1b[38;2;255;200;80m\x1b[1m"
	menuRowStyle   = "\x1b[48;2;20;20;26m\x1b[38;2;225;225;230m"
	menuFootStyle  = "\x1b[48;2;40;40;48m\x1b[38;2;140;140;150m"
)
