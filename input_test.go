package main

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Terminal input: where a drag whose release was lost is lifted, and CR LF
// from one Enter.
// ---------------------------------------------------------------------------

// touchAt decodes a captured touch message: its action (Android's 0 down,
// 1 up, 2 move) and the position it carries.
func touchAt(t *testing.T, w []byte) (action byte, x, y int32) {
	t.Helper()
	if len(w) != 32 || w[0] != ctrlInjectTouch {
		t.Fatalf("not a touch message: % x", w)
	}
	return w[1], int32(binary.BigEndian.Uint32(w[10:14])), int32(binary.BigEndian.Uint32(w[14:18]))
}

// TestLostReleaseLiftsWhereTheFingerIs: a press while a drag is still down
// means that drag's release never arrived. Its finger is lifted where the
// device last saw it (the press, then each move sent), not at the new press,
// which would drag it across the screen on the way up; then the new press
// goes down as before.
func TestLostReleaseLiftsWhereTheFingerIs(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.stream = &streamState{videoW: 1080, videoH: 1920}
	a.stream.updateGeometry()

	a.handleInput(mouseSeq(0, 30, 5, true))   // down
	a.handleInput(mouseSeq(32, 40, 10, true)) // drag: the first move is sent at once
	a.handleInput(mouseSeq(32, 41, 11, true)) // the next, within 8 ms, waits in the coalescer
	a.handleInput(mouseSeq(0, 10, 25, true))  // the release was lost; a press elsewhere

	n := len(cc.captured)
	if n < 4 {
		t.Fatalf("sent %d messages, want a down, a move, an up and a down", n)
	}
	_, lastX, lastY := touchAt(t, cc.captured[n-3])
	upAction, upX, upY := touchAt(t, cc.captured[n-2])
	downAction, downX, downY := touchAt(t, cc.captured[n-1])
	if upAction != 1 || downAction != 0 {
		t.Fatalf("the press sent actions %d then %d, want an up (1) then a down (0)", upAction, downAction)
	}
	if upX != lastX || upY != lastY {
		t.Errorf("the lost drag was lifted at %d,%d; the device last saw its finger at %d,%d", upX, upY, lastX, lastY)
	}
	if upX == downX && upY == downY {
		t.Errorf("the lost drag was lifted at the new press, %d,%d", downX, downY)
	}
	if !a.drag.isDown() {
		t.Error("the new press is not down")
	}

	// That press is lost too, before any move: it is lifted where it went
	// down.
	a.handleInput(mouseSeq(0, 60, 20, true))
	n = len(cc.captured)
	if _, upX, upY = touchAt(t, cc.captured[n-2]); upX != downX || upY != downY {
		t.Errorf("a lost press with no move was lifted at %d,%d, not where it went down, %d,%d", upX, upY, downX, downY)
	}
}

// feedTerminal hands b to the app as the terminal does: decoded into events
// the way inputLoop decodes what it reads, each handled as the run loop does.
func feedTerminal(a *app, b []byte) {
	for len(b) > 0 {
		ev, n := a.decodeInput(b)
		if n == 0 {
			return
		}
		b = b[n:]
		if ev != nil {
			a.handleInput(ev)
		}
	}
}

// TestCRLFIsOneEnter: a terminal that sends CR LF for Enter presses once
// wherever Enter is handled: the keyboard's focused key (one Enter on QUIT
// arms it, it does not arm and confirm it), the device (Enter, not Enter and
// Ctrl+J) and the menu (which closes, and the LF does not then reach the
// device). CR and LF on their own, and LF CR, decode as before.
func TestCRLFIsOneEnter(t *testing.T) {
	a := newTestApp(nil)
	if ev, n := a.decodeInput([]byte("\r\n")); string(ev) != "\r" || n != 2 {
		t.Errorf("CR LF decodes as %q from %d bytes, want one CR from both", ev, n)
	}
	for _, in := range []string{"\r", "\n", "\n\r", "\r\r", "\rx"} {
		if ev, n := a.decodeInput([]byte(in)); string(ev) != in[:1] || n != 1 {
			t.Errorf("%q decodes as %q from %d bytes, want %q from 1", in, ev, n, in[:1])
		}
	}

	withTermSize(t, 80, 30)
	k := newTestApp(newController(&capConn{}))
	k.tui = &tui{cols: 80, rows: 29}
	k.openKeyboard()
	k.kb.curRow = len(k.kb.rows) - 1
	for ci, c := range k.kb.rows[k.kb.curRow].cells {
		if c.key.label == "QUIT" {
			k.kb.curCol = ci
		}
	}
	feedTerminal(k, []byte("\r\n"))
	if quitQueued(k) {
		t.Error("one CR LF Enter on the focused QUIT armed and confirmed it")
	}
	if !k.kb.quitArmed(quitFromKeyboard, timeNowUnixNano()) {
		t.Error("one CR LF Enter on the focused QUIT did not arm it")
	}

	cc := &capConn{}
	d := newTestApp(newController(cc))
	feedTerminal(d, []byte("\r\n"))
	if len(cc.captured) != 2 {
		t.Errorf("CR LF sent %d messages to the device, want one Enter down and up", len(cc.captured))
	} else {
		for _, w := range cc.captured {
			if _, code, meta := keycoded(t, w); code != 66 || meta != 0 {
				t.Errorf("CR LF sent keycode %d meta %#x, want ENTER (66)", code, meta)
			}
		}
	}

	mc := &capConn{}
	m := newTestApp(newController(mc))
	m.tui = &tui{cols: 80, rows: 29}
	m.toggleMenu()
	feedTerminal(m, []byte("\r\n"))
	if m.menuOpen || len(mc.captured) != 0 {
		t.Errorf("CR LF in the menu: open=%v, sent % x to the device", m.menuOpen, mc.captured)
	}
}

// ---------------------------------------------------------------------------
// Typing beyond ASCII: a character with no Android key goes as text, also
// when the terminal's read splits its UTF-8 bytes, and without confusing the
// Alt chords, Esc, the menu or the on-screen keyboard.
// ---------------------------------------------------------------------------

// feedReads hands each read to the app as the input loop does: added to what
// the reads before it left over, decoded by d, and each event handled as the
// run loop handles it. It returns what is left over.
func feedReads(a *app, d *inputDecoder, reads ...string) []byte {
	var pend []byte
	for _, r := range reads {
		pend = d.feed(a, append(pend, r...), a.handleInput)
	}
	return pend
}

// wireLog says what captured control messages did, in order: "key N" for a
// keycode press (down then up), "text S" for injected text, "paste S" for a
// set-clipboard that pastes and "clip S" for one that does not.
func wireLog(t *testing.T, ws [][]byte) []string {
	t.Helper()
	var log []string
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		switch w[0] {
		case ctrlInjectKeycode:
			down, code, meta := keycoded(t, w)
			if down != 0 || i+1 == len(ws) {
				t.Fatalf("message %d: key %d is not a down followed by an up", i, code)
			}
			up, upCode, upMeta := keycoded(t, ws[i+1])
			if up != 1 || upCode != code || upMeta != meta {
				t.Fatalf("message %d: key %d down is followed by % x", i, code, ws[i+1])
			}
			i++
			entry := fmt.Sprintf("key %d", code)
			if meta != 0 {
				entry += fmt.Sprintf(" meta %#x", meta)
			}
			log = append(log, entry)
		case ctrlInjectText:
			if len(w) < 5 || int(binary.BigEndian.Uint32(w[1:5])) != len(w)-5 {
				t.Fatalf("message %d: inject text with a wrong length: % x", i, w)
			}
			log = append(log, "text "+string(w[5:]))
		case ctrlSetClipboard:
			_, paste, text := clipboardSet(t, w)
			if paste {
				log = append(log, "paste "+text)
			} else {
				log = append(log, "clip "+text)
			}
		default:
			log = append(log, fmt.Sprintf("type %d", w[0]))
		}
	}
	return log
}

// TestTypedNonASCIIGoesAsText: e-acute, n-tilde, the euro sign, a CJK
// character and an emoji typed in the terminal reach the device as text: one
// inject-text message holding exactly that character, and nothing else.
func TestTypedNonASCIIGoesAsText(t *testing.T) {
	for _, s := range []string{"é", "ñ", "€", "日", "😀"} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var d inputDecoder
		if left := feedReads(a, &d, s); len(left) != 0 {
			t.Errorf("typing %q left % x undecoded", s, left)
		}
		if got := wireLog(t, cc.captured); len(got) != 1 || got[0] != "text "+s {
			t.Errorf("typing %q sent %q, want one inject text of it", s, got)
		}
	}
}

// TestTypedNonASCIISplitAcrossReads: a read can end inside a character's UTF-8
// bytes. Nothing is sent until the rest arrives, the bytes so far wait (and a
// first byte alone is not taken for a lone Esc or for a key), and then the
// whole character goes as text, once.
func TestTypedNonASCIISplitAcrossReads(t *testing.T) {
	for _, s := range []string{"é", "€", "日", "😀"} {
		for i := 1; i < len(s); i++ {
			cc := &capConn{}
			a := newTestApp(newController(cc))
			var d inputDecoder
			left := feedReads(a, &d, s[:i])
			if string(left) != s[:i] || len(cc.captured) != 0 {
				t.Errorf("%q split after %d bytes: the first read left % x and sent % x, want it all left, nothing sent",
					s, i, left, cc.captured)
				continue
			}
			left = d.feed(a, append(left, s[i:]...), a.handleInput)
			if got := wireLog(t, cc.captured); len(left) != 0 || len(got) != 1 || got[0] != "text "+s {
				t.Errorf("%q split after %d bytes sent %q, left % x; want one inject text of it", s, i, got, left)
			}
		}
	}
	// An emoji in three reads, one of them a single continuation byte.
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	if left := feedReads(a, &d, "\xf0\x9f", "\x98", "\x80"); len(left) != 0 {
		t.Errorf("the emoji in three reads left % x", left)
	}
	if got := wireLog(t, cc.captured); len(got) != 1 || got[0] != "text 😀" {
		t.Errorf("the emoji in three reads sent %q", got)
	}
}

// TestTypedMixedASCIIAndNonASCII: in one read, ASCII letters and the space
// stay key presses (games and IMEs answer those) and punctuation stays text,
// while each character beyond ASCII goes as text, all in the order typed.
// Keycodes: Android's KEYCODE_A is 29 (so C 31, F 34), KEYCODE_SPACE 62.
func TestTypedMixedASCIIAndNonASCII(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	feedReads(a, &d, "café 😀!")
	want := []string{"key 31", "key 29", "key 34", "text é", "key 62", "text 😀", "text !"}
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("typing \"café 😀!\" sent %q, want %q", got, want)
	}
}

// TestInvalidUTF8StaysIgnored: bytes that are not UTF-8 (a stray continuation
// byte, 0xff, an overlong form, an encoded surrogate, a first byte followed by
// ASCII) type nothing, and the ASCII after them still types.
func TestInvalidUTF8StaysIgnored(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	left := feedReads(a, &d, "\x80", "\xff", "\xc0\xaf", "\xed\xa0\x80", "\xc3a")
	if got := wireLog(t, cc.captured); len(left) != 0 || len(got) != 1 || got[0] != "key 29" {
		t.Errorf("invalid bytes then \"a\" sent %q and left % x, want only the a (key 29)", got, left)
	}
}

// TestEscAndAltWithNonASCII: Alt with a character beyond ASCII (ESC then its
// UTF-8 bytes) is one chord, which does nothing, rather than a chord on its
// first byte with the rest left over as keys of their own; the next read can
// complete it. The Esc and Alt paths around it work as before: Alt+H is Home,
// Up is DPAD_UP, a lone Esc waits for the input loop's Esc window and is then
// Back, and a character typed after that Back is text.
func TestEscAndAltWithNonASCII(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	var events []string
	record := func(ev []byte) {
		events = append(events, string(ev))
		a.handleInput(ev)
	}
	if left := d.feed(a, []byte("\x1bé"+"x"), record); len(left) != 0 {
		t.Errorf("Alt+é then x left % x", left)
	}
	if strings.Join(events, "|") != "\x1bé|x" {
		t.Errorf("Alt+é then x decoded as %q, want the chord and then x", events)
	}
	if got := wireLog(t, cc.captured); len(got) != 1 || got[0] != "key 52" {
		t.Errorf("Alt+é then x sent %q, want only the x (key 52)", got)
	}

	events, cc.captured = nil, nil
	left := d.feed(a, []byte("\x1b\xe2\x82"), record)
	if string(left) != "\x1b\xe2\x82" || len(events) != 0 {
		t.Errorf("Alt+€ cut short decoded %q and left % x, want it all left", events, left)
	}
	if left = d.feed(a, append(left, "\xac"...), record); len(left) != 0 || strings.Join(events, "|") != "\x1b€" {
		t.Errorf("Alt+€ completed by the next read decoded as %q, left % x", events, left)
	}
	if len(cc.captured) != 0 {
		t.Errorf("Alt+€ sent % x", cc.captured)
	}

	cc.captured = nil
	feedReads(a, &d, "\x1bh", "\x1b[A")
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "key 3|key 19" {
		t.Errorf("Alt+H and Up sent %q, want HOME (3) and DPAD_UP (19)", got)
	}

	cc.captured = nil
	if left := feedReads(a, &d, "\x1b"); string(left) != "\x1b" || len(cc.captured) != 0 {
		t.Fatalf("a lone Esc left % x and sent % x, want it left for the Esc window", left, cc.captured)
	}
	a.handleInput([]byte{0x1b}) // the window passed: the input loop sends it alone
	feedReads(a, &d, "ñ")
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "type 4|type 4|text ñ" {
		t.Errorf("Esc, then ñ after the window, sent %q, want Back (down, up) then the text", got)
	}
}

// TestNonASCIIInTheOverlays: with the menu open, a character beyond ASCII
// closes it and is not typed, as any key the menu has no row for; with the
// on-screen keyboard open it reaches the device, as letters do.
func TestNonASCIIInTheOverlays(t *testing.T) {
	withTermSize(t, 80, 30)
	mc := &capConn{}
	m := newTestApp(newController(mc))
	m.tui = &tui{cols: 80, rows: 29}
	m.toggleMenu()
	var d inputDecoder
	feedReads(m, &d, "😀")
	if m.menuOpen || len(mc.captured) != 0 {
		t.Errorf("an emoji in the menu: open=%v, sent % x, want the menu closed and nothing sent", m.menuOpen, mc.captured)
	}

	kc := &capConn{}
	k := newTestApp(newController(kc))
	k.tui = &tui{cols: 80, rows: 29}
	k.openKeyboard()
	feedReads(k, &d, "é")
	if got := wireLog(t, kc.captured); !k.kb.open || len(got) != 1 || got[0] != "text é" {
		t.Errorf("é with the keyboard open: open=%v, sent %q, want it typed and the keyboard still open", k.kb.open, got)
	}
}
