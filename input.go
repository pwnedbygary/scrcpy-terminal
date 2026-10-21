package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// input loop: read stdin, decode escape sequences, dispatch
// ---------------------------------------------------------------------------

func (a *app) inputLoop() {
	defer func() {
		if r := recover(); r != nil {
			noticef("scterm: input loop panic: %v\n", r)
		}
	}()
	buf := make([]byte, 4096)
	pend := make([]byte, 0, 4096)
	var dec inputDecoder
	emit := func(ev []byte) {
		a.events <- inputEvent{kind: evBytes, buf: ev}
	}
	for {
		if !waitReadable(dec.wait(pend).Microseconds()) {
			pend = dec.expire(a, pend, emit)
			continue
		}
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		pend = dec.feed(a, append(pend, buf[:n]...), emit)
	}
}

// ---------------------------------------------------------------------------
// bracketed paste: while the terminal UI runs, the terminal sends a paste
// between pasteStart and pasteEnd (shellInit turns the mode on), so a paste is
// one event. It goes to the device as one paste, and nothing in it is read as
// a key or a shortcut, except Ctrl+C (raw mode reads it as a byte), which
// cancels it: the way out of a paste whose end marker never comes.
// ---------------------------------------------------------------------------

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	ctrlC      = 0x03
)

// maxPasteBytes is the longest paste sent: what one set-clipboard message
// carries. A longer one is dropped, and a notice says so.
const maxPasteBytes = clipboardTextMaxBytes

// maxTypedPasteBytes is the longest paste sent as injected text, where the
// device clipboard cannot take it: a peer's helper backend then types it key
// by key, pressing Enter at each line break, and nothing stops it once it is
// sent. 4096 bytes holds a long URL, an SSH public key or a page of text, in
// at most 14 messages. A longer paste is not sent, and a notice says so.
const maxTypedPasteBytes = 4096

// What the input loop waits for (inputDecoder.wait) besides input.
const (
	// escWait is how long an Esc waits for what follows it before it is
	// the Esc key; the rest of an escape sequence comes in the same write.
	escWait = 25 * time.Millisecond
	// pasteHintAfter is how long a paste can stay unfinished before a
	// notice says that Ctrl+C cancels it. The notice comes back every
	// pasteHintEvery, so it stays up for as long as the paste does.
	pasteHintAfter = 2 * time.Second
	pasteHintEvery = noticeShowFor / 2
	// cancelQuiet is the pause in input that ends a cancelled paste whose
	// end marker does not come.
	cancelQuiet = 250 * time.Millisecond
	// inputPoll is the longest wait for input.
	inputPoll = 500 * time.Millisecond
)

// inputDecoder decodes what the terminal sends into input events. From one
// read to the next it keeps the state of a paste (dropped as too long to send,
// with how many bytes of it so far; or cancelled), when to say that Ctrl+C
// cancels an unfinished one, and when the last read came.
type inputDecoder struct {
	dropping   bool
	dropped    int
	discarding bool         // what follows a cancelled paste's Ctrl+C is dropped
	hintAt     int64        // while a paste is unfinished, when the next Ctrl+C notice is due; else 0
	lastRead   int64        // when the last read came (see clock)
	now        func() int64 // the clock; nil is the real one
}

// inputClockStart is where the decoder's real clock starts. It holds a reading
// of the monotonic clock, so the time since then goes on as time does whatever
// the system time is set to, and setting it moves none of the deadlines.
var inputClockStart = time.Now()

// clock is the time now, in nanoseconds since inputClockStart.
func (d *inputDecoder) clock() int64 {
	if d.now != nil {
		return d.now()
	}
	return int64(time.Since(inputClockStart))
}

// feed decodes the events at the front of pend, hands each to emit, and
// returns what is left: the start of an event that a later read completes.
// The input loop calls it with each read, added to what the one before left.
func (d *inputDecoder) feed(a *app, pend []byte, emit func([]byte)) []byte {
	d.lastRead = d.clock()
	for len(pend) > 0 {
		if dbgControl != "" {
			fmt.Fprintf(os.Stderr, "scterm: raw %s\n", d.logged(pend))
		}
		ev, n := d.decode(a, pend)
		if n == 0 {
			break
		}
		pend = pend[n:]
		if ev != nil {
			emit(ev)
		}
	}
	return pend
}

// decode is decodeInput, except that a bracketed paste is one event, its
// markers included, however many reads it spans, and that an Esc just before
// one is the Esc key. A paste longer than maxPasteBytes is not kept: it is
// dropped as it comes in. A paste with a Ctrl+C in it is cancelled.
func (d *inputDecoder) decode(a *app, pend []byte) ([]byte, int) {
	switch {
	case d.discarding:
		return nil, d.discard(pend)
	case d.dropping:
		return nil, d.drop(a, pend)
	}
	body, ok := bytes.CutPrefix(pend, []byte(pasteStart))
	if !ok {
		return d.decodeKey(a, pend)
	}
	end := bytes.Index(body, []byte(pasteEnd))
	if c := ctrlCBefore(body, end); c >= 0 {
		return nil, len(pasteStart) + d.cancel(c)
	}
	if end >= 0 && end <= maxPasteBytes {
		d.hintAt = 0
		n := len(pasteStart) + end + len(pasteEnd)
		return pend[:n], n
	}
	if d.hintAt == 0 {
		d.hintAt = d.lastRead + int64(pasteHintAfter)
	}
	if len(body) < maxPasteBytes+len(pasteEnd) {
		return nil, 0 // the rest of the paste is still to come
	}
	d.dropping, d.dropped = true, 0
	return nil, len(pasteStart) + d.drop(a, body)
}

// decodeKey is decodeInput, but an Esc followed by a paste's start marker is
// the Esc key, not an Alt chord with the marker's Esc, and while what follows
// an Esc could still be the start of that marker, it waits for the rest
// (for escWait at most: see expire).
func (d *inputDecoder) decodeKey(a *app, pend []byte) ([]byte, int) {
	if pend[0] == 0x1b && len(pend) > 1 {
		if bytes.HasPrefix(pend[1:], []byte(pasteStart)) {
			return pend[:1], 1
		}
		if escWaiting(pend) {
			return nil, 0
		}
	}
	return a.decodeInput(pend)
}

// escWaiting reports whether pend is an Esc that waits for what follows it:
// alone, or with what could still become a paste's start marker.
func escWaiting(pend []byte) bool {
	return len(pend) > 0 && len(pend) <= len(pasteStart) && pend[0] == 0x1b &&
		bytes.HasPrefix([]byte(pasteStart), pend[1:])
}

// ctrlCBefore is where the first Ctrl+C in b is, before the end marker at end
// (-1: not in b), or -1 when there is none.
func ctrlCBefore(b []byte, end int) int {
	if end >= 0 {
		b = b[:end]
	}
	return bytes.IndexByte(b, ctrlC)
}

// cancel drops the paste whose Ctrl+C is at c in what is left of it: what
// comes before, and the Ctrl+C, are taken now, and what comes after is
// dropped up to the paste's end marker, or until input stops for cancelQuiet.
// A paste that holds a Ctrl+C character is so dropped whole, not typed.
func (d *inputDecoder) cancel(c int) int {
	d.dropping, d.discarding, d.hintAt = false, true, 0
	pasteNoticef("scterm: paste cancelled by Ctrl+C\n")
	return c + 1
}

// discard takes what follows a cancelled paste's Ctrl+C, up to and including
// its end marker. As in drop, the last bytes stay in case they begin the
// marker; expire drops them once input stops.
func (d *inputDecoder) discard(pend []byte) int {
	end := bytes.Index(pend, []byte(pasteEnd))
	if end < 0 {
		return max(len(pend)-(len(pasteEnd)-1), 0)
	}
	d.discarding = false
	return end + len(pasteEnd)
}

// drop takes the bytes of a paste too long to send, up to and including its
// end marker. Until the marker comes, the last bytes stay in case they are its
// beginning; so while dropping, at least that many are left over, never a
// lone Esc that the input loop would time out as a key.
func (d *inputDecoder) drop(a *app, pend []byte) int {
	end := bytes.Index(pend, []byte(pasteEnd))
	if c := ctrlCBefore(pend, end); c >= 0 {
		return d.cancel(c)
	}
	if end < 0 {
		n := max(len(pend)-(len(pasteEnd)-1), 0)
		d.dropped += n
		return n
	}
	d.dropped += end
	d.dropping, d.hintAt = false, 0
	if a.ctrl != nil { // without control no paste is sent, so none is missed
		pasteNoticef("scterm: paste not sent: %d bytes, more than the %d one paste can carry\n", d.dropped, maxPasteBytes)
	}
	return end + len(pasteEnd)
}

// wait is how long the input loop waits for its next read before it calls
// expire: until the next deadline, and inputPoll at most.
func (d *inputDecoder) wait(pend []byte) time.Duration {
	at, ok := d.deadline(pend)
	if !ok {
		return inputPoll
	}
	return min(max(time.Duration(at-d.clock()), 0), inputPoll)
}

// deadline is when expire next has something to do, if it has: end a
// cancelled paste once input has stopped, say that Ctrl+C cancels an
// unfinished one, or send an Esc that nothing followed.
func (d *inputDecoder) deadline(pend []byte) (int64, bool) {
	switch {
	case d.discarding:
		return d.lastRead + int64(cancelQuiet), true
	case d.hintAt != 0:
		return d.hintAt, true
	case escWaiting(pend):
		return d.lastRead + int64(escWait), true
	}
	return 0, false
}

// expire does what is due when no read came by the deadline, and returns what
// is left of pend. A cancelled paste ends, and what is left of it is dropped;
// a notice says (again) that Ctrl+C cancels an unfinished paste; an Esc that
// nothing followed is the Esc key, and with the start of a sequence after it,
// decodes as decodeInput decodes it.
func (d *inputDecoder) expire(a *app, pend []byte, emit func([]byte)) []byte {
	at, ok := d.deadline(pend)
	now := d.clock()
	if !ok || now < at {
		return pend
	}
	switch {
	case d.discarding:
		d.discarding = false
		return pend[:0]
	case d.hintAt != 0:
		d.hintAt = now + int64(pasteHintEvery)
		pasteNoticef("scterm: Ctrl+C cancels the unfinished paste\n")
		return pend
	case len(pend) == 1:
		emit([]byte{0x1b})
		return pend[:0]
	}
	for len(pend) > 0 {
		ev, n := a.decodeInput(pend)
		if n == 0 {
			break
		}
		pend = pend[n:]
		if ev != nil {
			emit(ev)
		}
	}
	return pend
}

// logged is pend as the debug log shows it: its bytes in hex, but of a paste
// only how long it is, never its text.
func (d *inputDecoder) logged(pend []byte) string {
	if d.dropping || d.discarding {
		return fmt.Sprintf("(%d bytes of a paste)", len(pend))
	}
	if i := bytes.Index(pend, []byte(pasteStart)); i >= 0 {
		return fmt.Sprintf("% x (then %d bytes from a paste)", pend[:i], len(pend)-i)
	}
	return fmt.Sprintf("% x", pend)
}

// isPaste reports whether b is a paste as inputDecoder hands it on. No other
// event is that long and bracketed by both markers.
func isPaste(b []byte) bool {
	return len(b) >= len(pasteStart)+len(pasteEnd) &&
		bytes.HasPrefix(b, []byte(pasteStart)) && bytes.HasSuffix(b, []byte(pasteEnd))
}

// pasteText is the text of a paste as the device should get it. Terminals
// send each line break in a paste as CR (some as CR LF), and Android text
// breaks lines at LF. Tabs stay; the other C0 control characters and DEL, and
// bytes that are not UTF-8, are left out: in a text field they are noise, and
// typed as text they would press keys. C1 characters (U+0080 to U+009F) are
// kept.
func pasteText(b []byte) string {
	s := string(b[len(pasteStart) : len(b)-len(pasteEnd)])
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// paste sends a paste to the device as one paste: onto the device clipboard
// and pasted from it where the device takes clipboard text from this computer,
// and as injected text (in as many messages as that takes) where it does not.
// A peer takes it only from the holder of its input lease (and says so only
// once per run), so without the lease it is not sent, and the notice says why.
func (a *app) paste(b []byte) {
	if a.ctrl == nil {
		return
	}
	text := pasteText(b)
	if text == "" {
		return
	}
	if a.sess != nil && a.sess.peer != nil && a.sess.peer.Lease() != peer.LeaseHeld {
		pasteNoticef("scterm: paste not sent: no control (--takeover takes it)\n")
		return
	}
	clip := a.clipboardPaste()
	if !clip && len(text) > maxTypedPasteBytes {
		pasteNoticef("scterm: paste not sent: %d bytes, at most %d typed\n", len(text), maxTypedPasteBytes)
		return
	}
	var err error
	if clip {
		err = a.ctrl.setClipboard(text, true)
	} else {
		err = a.ctrl.injectText(text)
	}
	if err != nil {
		noticef("scterm: paste: %v\n", err)
		return
	}
	if n := utf8.RuneCountInString(text); n == 1 {
		pasteNoticef("scterm: pasted 1 character\n")
	} else {
		pasteNoticef("scterm: pasted %d characters\n", n)
	}
}

// clipboardPaste reports whether a paste can go through the device clipboard:
// always with the scrcpy server, and with a peer whose pairing grants the
// clipboard and whose backend can set it (the target refuses it otherwise).
func (a *app) clipboardPaste() bool {
	if a.sess == nil || a.sess.peer == nil {
		return true
	}
	w := a.sess.peer.Welcome
	return slices.Contains(w.Grants, peer.GrantClipboard) &&
		w.Caps != nil && slices.Contains(w.Caps.Control, "set_clipboard")
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
	case c == 0x0d && len(pend) > 1 && pend[1] == 0x0a:
		// One Enter from a terminal that sends CR LF: one press, not a press
		// on CR and another on LF (which the device gets as Ctrl+J).
		return []byte{c}, 2
	case c == 0x0d || c == 0x0a:
		return []byte{c}, 1
	case c == 0x7f:
		return []byte{0x7f}, 1
	case c < 0x20:
		return []byte{c}, 1
	case c >= 0x80:
		// A character beyond ASCII is its whole UTF-8 sequence, which the
		// next read may complete. A byte that is not UTF-8 goes on alone, as
		// before, and nothing types it.
		if !utf8.FullRune(pend) {
			return nil, 0
		}
		if _, n := utf8.DecodeRune(pend); n > 1 {
			return pend[:n], n
		}
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
		if len(b) < 3 {
			return 0
		}
		if b[2] == 0x1b {
			return 2 // Alt+O: an Esc does not end SS3, it starts what follows
		}
		return 3
	default:
		// Alt+key: ESC + char, all of a character beyond ASCII, so none of
		// its bytes is left over to be read as a key of its own
		if b[1] >= 0x80 {
			if !utf8.FullRune(b[1:]) {
				return 0
			}
			if _, n := utf8.DecodeRune(b[1:]); n > 1 {
				return 1 + n
			}
		}
		return 2
	}
}

// handleInput dispatches decoded input. It must NEVER panic: any bad byte or
// transient nil-state swallows the event and keeps the TUI alive.
func (a *app) handleInput(b []byte) {
	defer func() {
		if r := recover(); r != nil {
			if isPaste(b) { // neither its text nor its length: a paste can hold a password
				noticef("scterm: input ignored: %v (a paste)\n", r)
				return
			}
			noticef("scterm: input ignored: %v (bytes % x)\n", r, b)
		}
	}()
	if len(b) == 0 {
		return
	}
	// An armed overlay Quit is for the press right after it; any other press
	// drops it.
	if isPress(b) {
		defer a.dropQuitArm(a.pendingQuit())
	}
	// global keys (work even when grabbed, before the keyboard consumes input).
	// NOTE: no Ctrl-Q! Zellij owns it (quits the session) — use Alt+Q.
	if len(b) == 1 {
		switch b[0] {
		case 0x07: // Ctrl-G
			a.toggleGrab()
			return
		case 0x0b: // Ctrl-K: show/hide the software keyboard
			a.toggleKeyboard()
			return
		case 0x0f: // Ctrl-O: also toggle the keyboard
			a.toggleKeyboard()
			return
		}
	}
	// A paste, or a typed character beyond ASCII, is text the menu has no row
	// for: it closes the menu, as such a key does, and is not typed.
	if a.menuOpen && (isPaste(b) || isTypedRune(b)) {
		a.closeMenu()
		return
	}
	// menu open: consume keys for its rows (modal, like the software keyboard)
	if a.menuOpen && a.menuKeyInput(b) {
		return
	}
	// keyboard open: consume keys for navigation, and mouse clicks for keys
	if a.kb != nil && a.kb.open {
		if a.keyboardInput(b) {
			return
		}
	}
	// mouse (SGR) sequences take priority
	if a.mouseEvent(b) {
		return
	}
	// F12 = grab toggle
	if isFKey(b, 12) {
		a.toggleGrab()
		return
	}
	// Alt+letter: the mnemonic device actions (actions.go appMnemonicOps).
	// These are the primary bindings on a phone keyboard, which has Alt but no
	// F-row. They run before the case-sensitive locals below so that each
	// chord has exactly one meaning: Alt+M is LOCAL mute, not device mute, and
	// Alt+G reaches grab here exactly as it does in the browser.
	if len(b) == 2 && b[0] == 0x1b && b[1] != '[' && b[1] != 'O' {
		if op, ok := mnemonicOp(b[1]); ok {
			switch op {
			case "keyboard":
				a.toggleKeyboard()
			case "toolbar":
				a.toggleMenu()
			case "screenshot":
				a.screenshot()
			case "resetvideo":
				// Keyframe requests are the one action that silently does
				// nothing without a control socket; say so, as the old
				// dedicated handler did.
				if a.ctrl == nil {
					noticef("scterm: Refresh video needs control, and this session is view only\n")
					return
				}
				a.applyAppOp(op)
			default:
				a.applyAppOp(op)
			}
			return
		}
	}
	// Alt+punctuation: local controls (Alt+M mute, Alt+- / Alt+= volume).
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
	// Text: a paste goes as one paste, and a typed character beyond ASCII,
	// which no Android key types, as text.
	if isPaste(b) {
		a.paste(b)
		return
	}
	if isTypedRune(b) {
		a.injectRune(b)
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
	if !a.grabbed {
		// With mouse reporting off no click can reach the bar, and no motion
		// will say the pointer has left it.
		a.hideBar()
	}
	a.refreshStatus()
}

// toggleLocalMute mutes the local audio stream, or brings back the volume it
// had before the mute (Alt+M).
func (a *app) toggleLocalMute() {
	if a.audio == nil || a.audio.err != nil {
		return // no sink to mute
	}
	a.audio.toggleMute()
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

// shotWait bounds how long a screenshot waits for the video goroutine before
// it saves the terminal's picture instead. The answer normally comes at once,
// from the frame the decoder holds. Where a read cannot be cut short, it
// comes with the next frame: the server's encoder repeats a frame 100 ms
// after it (ten times at most, so a still screen soon sends none), and ten
// such intervals leave room for a slow link.
var shotWait = time.Second

// screenshot saves the device's picture at the stream's resolution as a PNG
// (Alt+S). The video goroutine scales the frame; the PNG is encoded and written
// on a goroutine of its own, so neither the decoder nor the run loop waits.
func (a *app) screenshot() { a.takeShot(time.Now()) }

// takeShot is screenshot, with the time that names the file. The channel is
// closed once the shot is saved or dropped. One shot is taken at a time: a
// press while one is in flight, from its press until it is saved or dropped,
// takes none of its own and gets that shot's channel. So Alt+S held down
// (the terminal repeats the key) holds the memory of one shot, not of one
// per repeat, and the shot's own notice is the only one. Called on the run
// loop.
func (a *app) takeShot(at time.Time) <-chan struct{} {
	done := make(chan struct{})
	if a.tui == nil {
		close(done)
		return done // headless mode: nothing to capture
	}
	if a.stream != nil && a.stream.shotDone != nil {
		select {
		case <-a.stream.shotDone:
		default:
			return a.stream.shotDone // the shot in flight answers this press
		}
	}
	a.tui.mu.Lock()
	if !a.tui.running {
		a.tui.mu.Unlock()
		close(done)
		return done // before shell init, or after the terminal was given back
	}
	canvas := a.canvasShot()
	a.tui.mu.Unlock()
	switch {
	case a.sess != nil && a.sess.video == nil:
		noticef("scterm: screenshot: the video is off\n")
		close(done)
		return done
	case a.stream == nil || a.stream.frameCount.Load() == 0:
		noticef("scterm: screenshot: no frame yet\n")
		close(done)
		return done
	}
	reply := a.stream.requestShot()
	if reply == nil {
		close(done)
		return done // the video stream has ended, and with it the session
	}
	a.stream.shotDone = done
	go func() {
		defer close(done)
		a.saveShot(at, reply, canvas)
	}()
	return done
}

// canvasShot copies the picture out of the terminal's canvas, without the
// letterbox bars, or returns nil when there is no canvas of the current size.
// Called with a.tui.mu held.
func (a *app) canvasShot() *videoShot {
	t := a.tui
	cw, ch := t.cols, t.rows*2
	// The length check alone is not enough: a resize can keep the same cell
	// count, and a differently-shaped canvas would come out sheared. lastW and
	// lastH record the geometry the canvas was drawn at.
	if cw <= 0 || ch <= 0 || len(t.lastRGB) != cw*ch*4 || t.lastW != cw || t.lastH != ch {
		return nil
	}
	w, h, x, y := fitPicture(cw, ch, a.frameW, a.frameH)
	rgb := make([]byte, 0, w*h*4)
	for row := y; row < y+h; row++ {
		i := (row*cw + x) * 4
		rgb = append(rgb, t.lastRGB[i:i+w*4]...)
	}
	return &videoShot{rgb: rgb, w: w, h: h}
}

// saveShot waits for the picture on reply and saves it, or after shotWait the
// terminal's canvas. It saves nothing once the session is over: when the video
// stream ends first, or the terminal UI has ended by the time it would save.
func (a *app) saveShot(at time.Time, reply chan videoShot, canvas *videoShot) {
	timer := time.NewTimer(shotWait)
	defer timer.Stop()
	var shot videoShot
	late := false
	select {
	case got, ok := <-reply:
		if !ok {
			return
		}
		shot = got
	case <-timer.C:
		a.stream.cancelShot(reply)
		late = true
	}
	a.tui.mu.Lock()
	running := a.tui.running
	a.tui.mu.Unlock()
	switch {
	case !running:
		return
	case shot.err != nil:
		noticef("scterm: screenshot: %v\n", shot.err)
		return
	case late && canvas == nil:
		noticef("scterm: screenshot: no frame yet\n")
		return
	case late:
		shot = *canvas
	}
	name, err := writeShotPNG(at, shot.rgb, shot.w, shot.h)
	switch {
	case err != nil:
		noticef("scterm: screenshot: %v\n", err)
	case late:
		noticef("scterm: saved %s (%dx%d) at the terminal's size, because no frame came from the device in time\n", name, shot.w, shot.h)
	default:
		noticef("scterm: saved %s (%dx%d)\n", name, shot.w, shot.h)
	}
}

func (a *app) sendAndroidKey(code uint32) {
	a.sendAndroidKeyMeta(code, 0)
}

func (a *app) sendAndroidKeyMeta(code uint32, meta uint32) {
	if a.ctrl == nil {
		return
	}
	if err := a.ctrl.injectKey(code, meta); err != nil {
		noticef("scterm: inject key: %v\n", err)
	}
}

// handleByte: one raw byte (printable or control).
func (a *app) handleByte(c byte) {
	switch {
	case c == 0x0d: // Enter
		a.sendAndroidKeyMeta(66, 0) // ENTER
	case c == 0x7f: // Backspace
		a.sendAndroidKeyMeta(67, 0) // DEL
	case c == 0x09: // Tab
		a.sendAndroidKeyMeta(61, 0) // TAB
	case c >= 0x01 && c <= 0x1a:
		// Ctrl+A..Z -> keycode with AMETA_CTRL_ON (0x1000).
		// 0x01='A' ... 0x1a='Z'; letters are keycodes 29..54.
		letter := c - 1
		a.sendAndroidKeyMeta(uint32(29+letter), metaCtrlOn)
	case c >= 0x20 && c <= 0x7e:
		a.injectChar(c)
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
		noticef("scterm: inject text: %v\n", err)
	}
}

// isTypedRune reports whether b is one typed character beyond ASCII, as
// decodeInput hands it on: one whole UTF-8 sequence and nothing else.
func isTypedRune(b []byte) bool {
	if len(b) < 2 || b[0] < 0x80 {
		return false
	}
	_, n := utf8.DecodeRune(b)
	return n == len(b)
}

// injectRune sends a typed character beyond ASCII as text, as the browser's
// typing does.
func (a *app) injectRune(b []byte) {
	if a.ctrl == nil {
		return
	}
	if err := a.ctrl.injectText(string(b)); err != nil {
		noticef("scterm: inject text: %v\n", err)
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
			return 164, 0, true // VOLUME_MUTE (not 91: that is the microphone)
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

// leftPress reports whether an SGR mouse event is the left button going down.
// Drag motion (bit 32), wheel-up (64; wheel-down is 65) and buttons 8 to 11
// (128 to 131) also arrive as "M", and must not press an overlay button.
func leftPress(pressed bool, btnRaw int) bool {
	return pressed && btnRaw&(0x07|32|64|128) == 0
}

// sgrMouse parses an SGR mouse report, ESC[<b;x;y ending in M for a press or
// motion and in m for a release. The cell is 1-based.
func sgrMouse(b []byte) (btnRaw, cellX, cellY int, pressed, ok bool) {
	s := string(b)
	if !strings.HasPrefix(s, "\x1b[<") {
		return 0, 0, 0, false, false
	}
	end := strings.IndexByte(s, 'M')
	pressed = true
	if end < 0 {
		end = strings.IndexByte(s, 'm')
		pressed = false
	}
	if end < 0 {
		return 0, 0, 0, false, false
	}
	parts := strings.Split(s[3:end], ";")
	if len(parts) != 3 {
		return 0, 0, 0, false, false
	}
	btnRaw, _ = strconv.Atoi(parts[0])
	cellX, _ = strconv.Atoi(parts[1])
	cellY, _ = strconv.Atoi(parts[2])
	return btnRaw, max(cellX, 1), max(cellY, 1), pressed, true
}

// isPress reports whether decoded input presses something: any key, or a mouse
// button or the wheel going down. Motion and releases do not.
func isPress(b []byte) bool {
	btnRaw, _, _, pressed, ok := sgrMouse(b)
	return !ok || pressed && btnRaw&32 == 0
}

func (a *app) mouseEvent(b []byte) bool {
	btnRaw, cellX, cellY, pressed, ok := sgrMouse(b)
	if !ok {
		return false
	}

	// A press while a drag is still down means the drag's release never
	// arrived (let go outside the window, or dropped by a multiplexer). Lift
	// the device finger first, so it is not left down and this press is
	// handled as the new click it is rather than as part of that drag. It is
	// lifted where the device last saw it: lifted here, it would be dragged
	// across the screen on the way up.
	if leftPress(pressed, btnRaw) && a.drag.isDown() {
		at := a.drag.lastSent()
		a.drag.setDown(false)
		a.ctrl.touchRelease(at)
	}

	// Menu open: clicks on a row run it; clicks elsewhere are swallowed, so a
	// stray click never reaches the device through the overlay.
	if a.menuOpen {
		if leftPress(pressed, btnRaw) {
			_, rows := termSize()
			if item, ok := a.menuHitTest(cellX, cellY, rows); ok {
				a.runMenuRow(item)
			}
		}
		return true
	}

	// Keyboard open: clicks inside the keyboard area press keys, clicks
	// above it pass through to the device (tap-through). A drag that started
	// on the video keeps going to the device until it is released, wherever
	// the pointer is, or the device would be left with a finger down.
	if a.kb != nil && a.kb.open && !a.drag.isDown() {
		_, rows := termSize()
		if dbgControl != "" {
			fmt.Fprintf(stderrWriter(), "scterm: kb hit-test click (%d,%d) rows=%d open=%v\n", cellX, cellY, rows, a.kb.open)
		}
		key, ok := a.kb.hitTest(cellX, cellY, rows)
		if dbgControl != "" {
			fmt.Fprintf(stderrWriter(), "scterm: kb hit-test (%d,%d) -> key=%v ok=%v\n", cellX, cellY, key.label, ok)
		}
		if ok {
			if leftPress(pressed, btnRaw) {
				// flash the pressed key (we need its grid position: re-hit)
				if kc, ok2 := a.kb.hitTestKeyCell(cellX, cellY, rows); ok2 {
					a.kb.flash(kc.row, kc.col)
				}
				a.kb.act(a, key)
				a.refreshKeyboard()
			}
			return true
		}
		// The gap between two keys is still the keyboard: a near miss must
		// not tap the device behind it.
		if a.kb.coversRow(cellY, rows) {
			return true
		}
	}

	if !a.grabbed {
		return true
	}

	btn := btnRaw & 0x7f

	// Action bar: any mouse event wakes it -- hover motion where the terminal
	// forwards it (1003), and otherwise a click or wheel, so the strip is
	// discoverable even through a multiplexer that drops pure motion. A LEFT
	// click on either of its rows runs a button (or is swallowed) before it
	// can reach the device as a tap, but only if the bar was on screen before
	// the click: the click that wakes it is handled as if it were not there,
	// so without hover reporting nothing runs a button nobody has seen. Other
	// buttons and the wheel keep their normal meaning -- right is Back, wheel
	// scrolls -- even when they land on the strip, so the bar cannot hijack
	// them.
	shown := a.barVisible()
	if btnRaw&32 != 0 {
		a.barPointerY = cellY
	}
	a.wakeBar()
	if btn == 0 && shown {
		if a.barClick(cellX, cellY, pressed) {
			return true
		}
	}

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
		if a.drag.isDown() {
			a.coalescedMove(a.posAt(cellX, cellY))
		}
		return true
	}

	switch btn {
	case 0: // left
		if pressed {
			pos := a.posAt(cellX, cellY)
			a.drag.setDown(true)
			a.drag.sent(pos)
			a.ctrl.touch(true, pos)
		} else {
			a.drag.setDown(false)
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
