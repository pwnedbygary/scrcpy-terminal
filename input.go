package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ---------------------------------------------------------------------------
// input loop: read stdin, decode escape sequences, dispatch
// ---------------------------------------------------------------------------

func (a *app) inputLoop() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderrWriter(), "sct: input loop panic: %v\n", r)
		}
	}()
	buf := make([]byte, 4096)
	pend := make([]byte, 0, 4096)
	for {
		if len(pend) == 0 || len(pend) > 1 || pend[0] != 0x1b {
			// normal path: block until readable
			if !waitReadable(500 * 1000) {
				continue
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			pend = append(pend, buf[:n]...)
		} else {
			// pend == [ESC]: short window to catch a following sequence byte
			if !waitReadable(25 * 1000) {
				a.events <- inputEvent{kind: evBytes, buf: []byte{0x1b}}
				pend = pend[:0]
				continue
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			pend = append(pend, buf[:n]...)
		}
		for len(pend) > 0 {
			if dbgControl != "" {
				fmt.Fprintf(os.Stderr, "sct: raw % x\n", pend)
			}
			ev, consumed := a.decodeInput(pend)
			if consumed == 0 {
				break
			}
			pend = pend[consumed:]
			if ev != nil {
				a.events <- inputEvent{kind: evBytes, buf: ev}
			}
		}
	}
}

// waitReadable waits (up to usec microseconds) for stdin to become readable.
// Returns true if data is available.
func waitReadable(usec int64) bool {
	fds := []syscall.FdSet{}
	_ = fds
	var fdset syscall.FdSet
	fdset.Bits[0] = 1 << uint(syscall.Stdin%64)
	var tv syscall.Timeval
	tv.Usec = usec
	tv.Sec = usec / 1000000
	tv.Usec = usec % 1000000
	n, err := syscall.Select(syscall.Stdin+1, &fdset, nil, nil, &tv)
	return err == nil && n > 0
}

// decodeInput returns (event bytes or nil, consumed).
func (a *app) decodeInput(pend []byte) ([]byte, int) {
	c := pend[0]
	switch {
	case c == 0x1b:
		// escape sequence or bare escape
		if len(pend) < 2 {
			return nil, 0 // wait for more
		}
		want := escSeqLen(pend)
		if want == 0 {
			return nil, 0
		}
		if len(pend) >= want {
			return pend[:want], want
		}
		return nil, 0
	case c == 0x0d || c == 0x0a:
		return []byte{c}, 1
	case c == 0x7f:
		return []byte{0x7f}, 1
	case c < 0x20:
		return []byte{c}, 1
	default:
		return []byte{c}, 1
	}
}

// escSeqLen estimates how many bytes a CSI/SS3 sequence needs.
// Returns 0 if the sequence is incomplete (wait for more data).
func escSeqLen(b []byte) int {
	// b[0] == 0x1b
	if len(b) < 2 {
		return 0
	}
	switch b[1] {
	case '[': // CSI
		// CSI params may contain 0x30-0x3f (numeric/private) and
		// intermediates 0x20-0x2f; the sequence ends at 0x40-0x7e.
		// If we see a byte that can never start a CSI sequence, stop and
		// treat what we have as a bare ESC + raw bytes.
		for i := 2; i < len(b); i++ {
			c := b[i]
			if c >= 0x40 && c <= 0x7e {
				return i + 1
			}
			if c < 0x20 {
				return 2 // incomplete/garbage: emit ESC alone
			}
		}
		return 0
	case 'O': // SS3
		if len(b) >= 3 {
			return 3
		}
		return 0
	default:
		// Alt+key: ESC + char
		return 2
	}
}

// handleInput dispatches decoded input. It must NEVER panic: any bad byte or
// transient nil-state swallows the event and keeps the TUI alive.
func (a *app) handleInput(b []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderrWriter(), "sct: input ignored: %v (bytes % x)\n", r, b)
		}
	}()
	if len(b) == 0 {
		return
	}
	// palette open: local navigation only
	if a.paletteOpen {
		a.paletteInput(b)
		return
	}
	// mouse (SGR) sequences take priority
	if a.mouseEvent(b) {
		return
	}
	// global keys (work even when grabbed)
	if len(b) == 1 {
		switch b[0] {
		case 0x11: // Ctrl-Q
			a.events <- inputEvent{kind: evQuit}
			return
		case 0x07: // Ctrl-G
			a.toggleGrab()
			return
		case 0x0b: // Ctrl-K
			a.openPalette()
			return
		case 0x1b: // bare Alt-Esc? treat as Esc (back)
		}
		// bare ESC (single byte) came through as 0x1b only if there is no
		// sequence; decodeInput only returns 0x1b with a following char.
	}
	// F12 = grab toggle
	if isFKey(b, 12) {
		a.toggleGrab()
		return
	}
	// Alt+key: local controls (Alt+M mute, Alt+- / Alt+= volume)
	if len(b) == 2 && b[0] == 0x1b && b[1] != '[' && b[1] != 'O' {
		switch b[1] {
		case 'm', 'M':
			a.toggleLocalMute()
			return
		case '-', '_':
			a.adjustLocalVolume(-10)
			return
		case '=', '+':
			a.adjustLocalVolume(10)
			return
		}
	}
	// F-keys -> Android keys
	if code, ok := fKeyMap(b); ok {
		a.mapAppFKey(code)
		return
	}
	// Esc alone = Back
	if string(b) == "\x1b" {
		if a.ctrl != nil {
			a.ctrl.back()
		}
		return
	}

	if !a.grabbed && a.inZellij {
		// When ungrab is explicit in Zellij, still pass nothing.
		// (Keys before F12 belong to Zellij anyway.)
		return
	}

	// Single bytes: printable text / control keys
	if len(b) == 1 {
		a.handleByte(b[0])
		return
	}
	// Sequences: arrows, home/end, etc.
	if code, meta, ok := seqKeyMap(b); ok {
		a.sendAndroidKeyMeta(code, meta)
		return
	}
}

func (a *app) toggleGrab() {
	a.setMouse(!a.grabbed)
	a.refreshStatus()
}

// toggleLocalMute mutes/unmutes the local audio stream (Alt+M).
func (a *app) toggleLocalMute() {
	if a.audio == nil || a.audio.err != nil {
		return // no sink to mute
	}
	if a.audio.gainPercent() == 0 {
		a.audio.setGain(100)
	} else {
		a.audio.setGain(0)
	}
	a.refreshStatus()
}

// adjustLocalVolume changes local playback volume (Alt+- / Alt+=).
func (a *app) adjustLocalVolume(delta int) {
	if a.audio == nil || a.audio.err != nil {
		return // no sink to adjust
	}
	a.audio.setGain(a.audio.gainPercent() + delta)
	a.refreshStatus()
}

func (a *app) sendAndroidKey(code uint32) {
	a.sendAndroidKeyMeta(code, 0)
}

func (a *app) sendAndroidKeyMeta(code uint32, meta uint32) {
	if a.ctrl == nil {
		return
	}
	if err := a.ctrl.injectKey(code, meta); err != nil {
		fmt.Fprintf(stderrWriter(), "sct: inject key: %v\n", err)
	}
}

// handleByte: one raw byte (printable or control).
func (a *app) handleByte(c byte) {
	switch c {
	case 0x0d: // Enter
		a.sendAndroidKeyMeta(66, metaCtrlOn&0) // ENTER
	case 0x7f: // Backspace
		a.sendAndroidKeyMeta(67, 0) // DEL
	default:
		if c >= 0x20 && c <= 0x7e {
			a.injectChar(c)
		}
	}
}

// injectChar sends a printable ASCII char. Letters/digits/space are sent as
// key events (games respond), others go as text.
func (a *app) injectChar(c byte) {
	if a.ctrl == nil {
		return
	}
	if c == ' ' {
		a.sendAndroidKeyMeta(62, 0) // SPACE
		return
	}
	if c >= 'a' && c <= 'z' {
		a.sendAndroidKeyMeta(uint32(29+c-'a'), 0)
		return
	}
	if c >= 'A' && c <= 'Z' {
		a.sendAndroidKeyMeta(uint32(29+c-'A'), metaShiftOn)
		return
	}
	if c >= '0' && c <= '9' {
		code := uint32(7 + (c - '0'))
		meta := uint32(0)
		switch c {
		case '1':
			code, meta = 8, 0
		}
		_ = code
		// digits: shift gives symbols; plain digit = no meta
		a.sendAndroidKeyMeta(uint32(7+(c-'0')), meta)
		return
	}
	// Other printable: text injection.
	if err := a.ctrl.injectText(string(c)); err != nil {
		fmt.Fprintf(stderrWriter(), "sct: inject text: %v\n", err)
	}
}

// ---------------------------------------------------------------------------
// key maps
// ---------------------------------------------------------------------------

// F1  home, F2 menu, F3 recents, F4 power, F5 vol- ... F7 mute, F8 rotate,
// F9 notif, F10 settings, F11 collapse. F12 = grab (handled separately).
func fKeyMap(b []byte) (uint32, bool) {
	s := string(b)
	// SS3 form for F1..F4
	switch s {
	case "\x1bOP":
		return 131, true // F1
	case "\x1bOQ":
		return 132, true // F2
	case "\x1bOR":
		return 133, true // F3
	case "\x1bOS":
		return 134, true // F4
	}
	if !strings.HasPrefix(s, "\x1b[") {
		return 0, false
	}
	if strings.HasSuffix(s, "~") {
		body := s[2 : len(s)-1]
		n, err := strconv.Atoi(body)
		if err != nil {
			return 0, false
		}
		// xterm: 11=F1..15=F5, 17=F6..21=F10, 23=F11, 24=F12
		switch n {
		case 11:
			return 131, true // F1
		case 12:
			return 132, true
		case 13:
			return 133, true
		case 14:
			return 134, true
		case 15:
			return 135, true // F5
		case 17:
			return 136, true // F6
		case 18:
			return 137, true // F7
		case 19:
			return 138, true // F8
		case 20:
			return 139, true // F9
		case 21:
			return 140, true // F10
		case 23:
			return 141, true // F11
		case 24:
			return 142, true // F12
		}
	}
	return 0, false
}

func isFKey(b []byte, n int) bool {
	s := string(b)
	if s == fmt.Sprintf("\x1b[%d~", n+10) && n == 12 {
		return true
	}
	switch n {
	case 1:
		return s == "\x1bOP" || s == "\x1b[11~"
	case 12:
		return s == "\x1b[24~"
	}
	return false
}

// androidKeyForF maps the F-keys we named to Android keycodes.
// (F1 home, F2 menu, F3 app-switch, F4 power, F5 vol-down, F6 vol-up,
//
//	F7 mute, F8 rotate, F9 notif, F10 settings, F11 collapse)
func fKeyToAction(code uint32) (action string) {
	switch code {
	case 131:
		return "KEYCODE_HOME"
	case 132:
		return "KEYCODE_MENU"
	case 133:
		return "KEYCODE_APP_SWITCH"
	case 134:
		return "KEYCODE_POWER"
	case 135:
		return "KEYCODE_VOLUME_DOWN"
	case 136:
		return "KEYCODE_VOLUME_UP"
	case 137:
		return "KEYCODE_MUTE"
	case 138:
		return "KEYCODE_CAMERA" // F8 -> custom in app
	}
	return ""
}

// seqKeyMap handles arrows, home/end, pgup/pgdn, etc.
func seqKeyMap(b []byte) (uint32, uint32, bool) {
	s := string(b)
	meta := uint32(0)
	base := s
	if strings.HasPrefix(base, "\x1b[1;") {
		// modifier variant e.g. ESC[1;5A (ctrl-up)
		parts := strings.Split(base[2:], ";")
		if len(parts) == 3 {
			base = "\x1b[" + parts[0] + parts[2]
			switch parts[1] {
			case "2":
				meta = metaShiftOn
			case "3":
				meta = metaAltOn
			case "5":
				meta = metaCtrlOn
			}
		}
	}
	switch base {
	case "\x1b[A", "\x1bOA":
		return 19, meta, true // DPAD_UP
	case "\x1b[B", "\x1bOB":
		return 20, meta, true // DPAD_DOWN
	case "\x1b[C", "\x1bOC":
		return 22, meta, true // DPAD_RIGHT
	case "\x1b[D", "\x1bOD":
		return 21, meta, true // DPAD_LEFT
	case "\x1b[H", "\x1b[1~", "\x1b[7~":
		return 122, meta, true // MOVE_HOME
	case "\x1b[F", "\x1b[4~", "\x1b[8~":
		return 123, meta, true // MOVE_END
	case "\x1b[5~":
		return 92, meta, true // PAGE_UP
	case "\x1b[6~":
		return 93, meta, true // PAGE_DOWN
	case "\x1b[2~":
		return 0, meta, false // Insert: ignore
	case "\x1b[3~":
		return 112, meta, true // FORWARD_DEL
	case "\x1b[Z":
		return 61, metaShiftOn, true // Shift-Tab (backtab)
	}
	// F-key fallback
	if code, ok := fKeyMap(b); ok {
		switch code {
		case 131:
			return 3, 0, true // HOME
		case 132:
			return 82, 0, true // MENU
		case 133:
			return 187, 0, true // APP_SWITCH
		case 134:
			return 26, 0, true // POWER
		case 135:
			return 25, 0, true // VOLUME_DOWN
		case 136:
			return 24, 0, true // VOLUME_UP
		case 137:
			return 91, 0, true // MUTE
		case 138:
			return 3, 0, false // F8 handled in app (rotate)
		case 139:
			return 3, 0, false // F9 handled in app (notif)
		case 140:
			return 3, 0, false // F10 handled in app (settings)
		case 141:
			return 3, 0, false // F11 handled in app (collapse)
		}
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// mouse: SGR escape sequences ESC[<b;x;yM / m
// ---------------------------------------------------------------------------

func (a *app) mouseEvent(b []byte) bool {
	s := string(b)
	if !strings.HasPrefix(s, "\x1b[<") {
		return false
	}
	// parse b;x;y (M = press/motion, m = release)
	var parts []string
	end := strings.IndexByte(s, 'M')
	pressed := true
	if end < 0 {
		end = strings.IndexByte(s, 'm')
		pressed = false
	}
	if end < 0 {
		return false
	}
	parts = strings.Split(s[3:end], ";")
	if len(parts) != 3 {
		return false
	}
	btnRaw, _ := strconv.Atoi(parts[0])
	cellX, _ := strconv.Atoi(parts[1]) // 1-based
	cellY, _ := strconv.Atoi(parts[2])
	if cellX <= 0 {
		cellX = 1
	}
	if cellY <= 0 {
		cellY = 1
	}

	if !a.grabbed {
		return true
	}

	btn := btnRaw & 0x7f

	if a.ctrl == nil {
		// No control socket (--control=false or not yet connected):
		// consume mouse events, never crash on them.
		return true
	}

	switch {
	case btn == 64:
		a.scrollAt(cellX, cellY, 1)
		return true
	case btn == 65:
		a.scrollAt(cellX, cellY, -1)
		return true
	case btnRaw&32 != 0 && pressed: // motion
		if a.mouseDown {
			a.ctrl.touchMove(a.posAt(cellX, cellY))
		}
		return true
	}

	switch btn {
	case 0: // left
		if pressed {
			a.mouseDown = true
			a.ctrl.touch(true, a.posAt(cellX, cellY))
		} else {
			a.mouseDown = false
			a.ctrl.touchRelease(a.posAt(cellX, cellY))
		}
	case 1: // middle
		if pressed {
			a.sendAndroidKeyMeta(3, 0) // HOME
		}
	case 2: // right: back
		if pressed {
			a.ctrl.back()
		}
	}
	return true
}

var _ = fmt.Sprintf
