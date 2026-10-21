package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"unicode/utf8"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// Bracketed paste: while the terminal UI owns the terminal, a paste arrives
// between markers and goes to the device as one paste, never as keys.
//
// Independent facts these tests use: the markers are xterm's (mode 2004:
// ESC [ 200 ~ before a paste, ESC [ 201 ~ after it); scrcpy's set-clipboard
// message is type 9, a u64 sequence, a u8 paste flag, a u32 length and the
// UTF-8 text, and its text can be 262130 bytes at most (SC_CONTROL_MSG_MAX_SIZE,
// 1 << 18, less its 14-byte header); one inject-text message (type 1) carries
// at most 300 bytes (SC_CONTROL_MSG_INJECT_TEXT_MAX_LENGTH).
// ---------------------------------------------------------------------------

const scrcpyClipboardMax = 1<<18 - 14

// pasted is text as a terminal pastes it in bracketed-paste mode.
func pasted(text string) string { return "\x1b[200~" + text + "\x1b[201~" }

// readsOf cuts s into reads of n bytes, as a terminal delivers a long paste.
func readsOf(s string, n int) []string {
	var reads []string
	for len(s) > n {
		reads = append(reads, s[:n])
		s = s[n:]
	}
	return append(reads, s)
}

// clipboardSet decodes a captured set-clipboard message.
func clipboardSet(t *testing.T, w []byte) (seq uint64, paste bool, text string) {
	t.Helper()
	if len(w) < 14 || w[0] != 9 || int(binary.BigEndian.Uint32(w[10:14])) != len(w)-14 {
		t.Fatalf("not a set-clipboard message: % x", w[:min(len(w), 32)])
	}
	return binary.BigEndian.Uint64(w[1:9]), w[9] != 0, string(w[14:])
}

// onePaste returns what the captured messages pasted, failing unless the
// device got exactly one set-clipboard, pasting, with no acknowledgement asked.
func onePaste(t *testing.T, ws [][]byte) string {
	t.Helper()
	if len(ws) != 1 {
		t.Fatalf("the device got %d messages, want one set-clipboard (first: % x)", len(ws), first(ws))
	}
	seq, paste, text := clipboardSet(t, ws[0])
	if seq != 0 || !paste {
		t.Fatalf("set-clipboard with sequence %d, paste %v; want 0 (no ack) and paste", seq, paste)
	}
	return text
}

// first is the start of the first captured message, for a failure message.
func first(ws [][]byte) []byte {
	if len(ws) == 0 {
		return nil
	}
	return ws[0][:min(len(ws[0]), 16)]
}

// TestBracketedPasteWhileTheUIOwnsTheScreen: bracketed paste is on from
// shellInit to shellClose, and giving the terminal back turns it off, also
// after a panic on another goroutine.
func TestBracketedPasteWhileTheUIOwnsTheScreen(t *testing.T) {
	tr := &tui{cols: 20, rows: 4}
	if out := captureStdout(t, tr.shellInit); !strings.Contains(out, "\x1b[?2004h") {
		t.Errorf("shellInit leaves bracketed paste off: %q", out)
	}
	if out := captureStdout(t, tr.shellClose); !strings.Contains(out, "\x1b[?2004l") {
		t.Errorf("shellClose leaves bracketed paste on: %q", out)
	}

	a := newTestApp(nil)
	a.tui = &tui{cols: 20, rows: 4, running: true}
	a.restoreTerm = a.terminalRestorer(nil)
	out := captureTerminal(t, func() {
		defer func() { _ = recover() }()
		defer a.restoreTerminalOnPanic()
		panic("boom")
	})
	if !strings.Contains(out, "\x1b[?2004l") {
		t.Errorf("the restore after a panic leaves bracketed paste on: %q", out)
	}
}

// TestPasteIsOnePaste: a paste reaches the device as one set-clipboard that
// pastes, not key by key. The terminal sends each line break as CR (or CR LF);
// the device gets LF. A notice says how many characters, never which.
func TestPasteIsOnePaste(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	var left []byte
	out := captureStderr(t, func() { left = feedReads(a, &d, pasted("ls -la\rcd /tmp\r\nécho 😀\tend")) })
	want := "ls -la\ncd /tmp\nécho 😀\tend"
	if got := onePaste(t, cc.captured); got != want || len(left) != 0 {
		t.Errorf("pasted %q (left % x), want %q", got, left, want)
	}
	if note := fmt.Sprintf("pasted %d characters", utf8.RuneCountInString(want)); !strings.Contains(out, note) {
		t.Errorf("the notice is %q, want it to say %q", out, note)
	}
	if strings.Contains(out, "cd /tmp") {
		t.Errorf("the notice shows the pasted text: %q", out)
	}
}

// TestPasteSplitAcrossReads: the markers and the text can be cut anywhere by
// the terminal's reads. Nothing is sent before the end marker is in, and then
// the paste goes once, whole, with the input after it typed as before.
func TestPasteSplitAcrossReads(t *testing.T) {
	const text = "héllo\rwörld 😀"
	p := pasted(text)
	for i := 1; i < len(p); i++ {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var d inputDecoder
		left := feedReads(a, &d, p[:i])
		if len(cc.captured) != 0 {
			t.Fatalf("cut after %d bytes: the first read sent %d messages before the paste ended", i, len(cc.captured))
		}
		captureStderr(t, func() { left = d.feed(a, append(left, p[i:]+"x"...), a.handleInput) })
		if got := wireLog(t, cc.captured); len(left) != 0 || strings.Join(got, "|") != "paste héllo\nwörld 😀|key 52" {
			t.Errorf("cut after %d bytes: sent %q, left % x; want the paste then the x", i, got, left)
		}
	}

	// In three reads, each cut inside a marker.
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	captureStderr(t, func() { feedReads(a, &d, "\x1b[20", "0~"+text+"\x1b[2", "01~") })
	if got := onePaste(t, cc.captured); got != "héllo\nwörld 😀" {
		t.Errorf("in three reads the paste is %q", got)
	}
}

// TestPasteLongerThanOneMessage: one set-clipboard carries a paste far longer
// than one inject-text message, up to scrcpy's limit for its text, in one
// piece. One byte more and nothing is sent, a notice gives the size, the
// paste is dropped up to its end marker however the reads cut it (nothing in
// it is typed, not even an Alt+Q), and what follows is typed as before.
func TestPasteLongerThanOneMessage(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	long := strings.Repeat("ab€ ", 250) // 1500 bytes
	captureStderr(t, func() { feedReads(a, &d, pasted(long)) })
	if got := onePaste(t, cc.captured); got != long {
		t.Errorf("a %d-byte paste arrived as %d bytes", len(long), len(got))
	}

	cc.captured = nil
	most := strings.Repeat("z", scrcpyClipboardMax)
	captureStderr(t, func() { feedReads(a, &d, readsOf(pasted(most), 4096)...) })
	if got := onePaste(t, cc.captured); got != most {
		t.Errorf("the longest paste (%d bytes) arrived as %d bytes", len(most), len(got))
	}

	// One byte too long, and long enough to be dropped while its end marker
	// is still to come; in 4096-byte reads, and with a read that ends two
	// bytes into the end marker.
	tooLong := "\x1bq" + strings.Repeat("y", scrcpyClipboardMax-1)
	for _, over := range []string{tooLong, tooLong + strings.Repeat("y", 4999)} {
		input := pasted(over) + "x"
		inMarker := len(input) - len("\x1b[201~x") + 2
		for _, reads := range [][]string{readsOf(input, 4096), append(readsOf(input[:inMarker], 4096), input[inMarker:])} {
			cc.captured = nil
			var left []byte
			out := captureStderr(t, func() { left = feedReads(a, &d, reads...) })
			if got := wireLog(t, cc.captured); len(left) != 0 || len(got) != 1 || got[0] != "key 52" {
				t.Errorf("%d bytes in %d reads: sent %d messages (first: % x) and left % x; want only the x after it",
					len(over), len(reads), len(cc.captured), first(cc.captured), left)
			}
			if quitQueued(a) {
				t.Errorf("%d bytes in %d reads: the dropped paste was typed (Alt+Q queued a quit)", len(over), len(reads))
			}
			if note := fmt.Sprintf("paste not sent: %d bytes", len(over)); !strings.Contains(out, note) {
				t.Errorf("%d bytes in %d reads: the notice is %q, want it to say %q", len(over), len(reads), out, note)
			}
		}
	}
}

// TestDroppingAPasteNeverLeavesALoneEsc: while a paste too long to send is
// dropped, the bytes left over between reads are never a lone Esc, which the
// input loop would send as Back once its Esc window passed, cutting the end
// marker in two and leaving everything after it dropped.
func TestDroppingAPasteNeverLeavesALoneEsc(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	p := pasted(strings.Repeat("y", scrcpyClipboardMax+10)) + "x"
	cut := len(p) - 9 // the last two bytes of the paste, its end marker and the x come a byte per read
	var left []byte
	loneEsc := false
	captureStderr(t, func() {
		left = feedReads(a, &d, p[:cut])
		for _, c := range []byte(p[cut:]) {
			loneEsc = loneEsc || string(left) == "\x1b"
			left = d.feed(a, append(left, c), a.handleInput)
		}
	})
	if loneEsc {
		t.Error("a lone Esc was left over while the paste was dropped")
	}
	if got := wireLog(t, cc.captured); len(left) != 0 || len(got) != 1 || got[0] != "key 52" {
		t.Errorf("after the dropped paste: sent %d messages (first: % x), left % x; want only the x",
			len(cc.captured), first(cc.captured), left)
	}
}

// TestPasteWithoutTheDeviceClipboard: a peer whose pairing does not grant the
// clipboard, or whose backend cannot set it, refuses set-clipboard; there the
// paste goes as inject-text messages instead, each within the 300-byte limit
// and none cutting a UTF-8 sequence, which together carry the whole text. A
// peer with both still gets one set-clipboard. (Grant and capability names
// from docs/PEER_PROTOCOL.md.)
func TestPasteWithoutTheDeviceClipboard(t *testing.T) {
	text := strings.Repeat("é😀a", 200) // 1400 bytes
	for _, c := range []struct {
		name    string
		grants  []string
		control []string
		clip    bool
	}{
		{"no clipboard grant", []string{"view", "control"}, []string{"inject_text", "set_clipboard"}, false},
		{"backend without set_clipboard", []string{"view", "control", "clipboard"}, []string{"inject_text"}, false},
		{"clipboard granted and settable", []string{"view", "control", "clipboard"}, []string{"inject_text", "set_clipboard"}, true},
	} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		a.sess = &session{peer: &peer.Session{Welcome: peer.Message{Grants: c.grants, Caps: &peer.Capabilities{Control: c.control}}}}
		var d inputDecoder
		captureStderr(t, func() { feedReads(a, &d, pasted(text)) })
		if c.clip {
			if got := onePaste(t, cc.captured); got != text {
				t.Errorf("%s: the paste arrived as %d bytes", c.name, len(got))
			}
			continue
		}
		var got strings.Builder
		for i, w := range cc.captured {
			if len(w) < 5 || w[0] != 1 || int(binary.BigEndian.Uint32(w[1:5])) != len(w)-5 || len(w)-5 > 300 || !utf8.Valid(w[5:]) {
				t.Fatalf("%s: message %d is not a valid inject text of at most 300 bytes: % x", c.name, i, w[:min(len(w), 16)])
			}
			got.Write(w[5:])
		}
		if len(cc.captured) < 2 || got.String() != text {
			t.Errorf("%s: %d messages carried %d bytes, want the %d-byte text in several", c.name, len(cc.captured), got.Len(), len(text))
		}
	}
}

// TestPasteIsNeverKeys: what is pasted is text, even where it looks like keys
// to this program (Ctrl-G grab, Ctrl-K keyboard, Alt+Q quit, Alt+/ menu, F12,
// a mouse press): none of them acts, and the text goes without its control
// characters (a tab stays).
func TestPasteIsNeverKeys(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 80, rows: 29}
	a.stream = &streamState{videoW: 1080, videoH: 1920}
	a.stream.updateGeometry()
	var d inputDecoder
	captureTerminal(t, func() {
		feedReads(a, &d, pasted("a\x07b\x0bc\x1bqd\x1b/e\x1b[24~f\x1b[<0;5;5Mg\th\x7f"))
	})
	quit := quitQueued(a)
	if !a.grabbed || a.kb.open || a.menuOpen || quit || a.drag.isDown() {
		t.Errorf("pasted keys acted: grabbed=%v keyboard=%v menu=%v quit=%v touch down=%v",
			a.grabbed, a.kb.open, a.menuOpen, quit, a.drag.isDown())
	}
	if got := onePaste(t, cc.captured); got != "abcqd/e[24~f[<0;5;5Mg\th" {
		t.Errorf("the paste arrived as %q", got)
	}
}

// TestPasteInTheOverlaysAndUngrabbed: with the menu open a paste closes it and
// is not sent, as any key the menu has no row for; with the on-screen keyboard
// open it is sent, as typing is; ungrabbed in Zellij, where keys go nowhere,
// it goes nowhere either.
func TestPasteInTheOverlaysAndUngrabbed(t *testing.T) {
	withTermSize(t, 80, 30)

	mc := &capConn{}
	m := newTestApp(newController(mc))
	m.tui = &tui{cols: 80, rows: 29}
	m.toggleMenu()
	feedReads(m, &inputDecoder{}, pasted("secret"))
	if m.menuOpen || len(mc.captured) != 0 {
		t.Errorf("a paste in the menu: open=%v, sent %d messages; want the menu closed and nothing sent", m.menuOpen, len(mc.captured))
	}

	kc := &capConn{}
	k := newTestApp(newController(kc))
	k.tui = &tui{cols: 80, rows: 29}
	k.openKeyboard()
	captureStderr(t, func() { feedReads(k, &inputDecoder{}, pasted("hi")) })
	if !k.kb.open {
		t.Error("a paste closed the on-screen keyboard")
	}
	if got := onePaste(t, kc.captured); got != "hi" {
		t.Errorf("a paste with the keyboard open sent %q", got)
	}

	zc := &capConn{}
	z := newTestApp(newController(zc))
	z.inZellij, z.grabbed = true, false
	feedReads(z, &inputDecoder{}, pasted("hi"), "a")
	if len(zc.captured) != 0 {
		t.Errorf("ungrabbed in Zellij a paste and a key sent %d messages", len(zc.captured))
	}
}

// TestTextLeavesTheActionBarAlone: the action bar answers the mouse. A
// character beyond ASCII and a paste, typed while it shows, press none of its
// buttons and do not take it down; they reach the device as text.
func TestTextLeavesTheActionBarAlone(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	captureStderr(t, func() { feedReads(a, &inputDecoder{}, "é", pasted("x\ry")) })
	if !a.barVisible() {
		t.Error("typing took the action bar down")
	}
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "text é|paste x\ny" {
		t.Errorf("with the action bar up, typing sent %q, want the text and the paste only", got)
	}
}

// TestPasteWithoutControl: with control off (--control=false, or a view-only
// peer, both of which leave no controller) a paste, also one too long to send,
// does nothing and says nothing, as keys do, and nothing panics; nor with a
// controller that has no socket.
func TestPasteWithoutControl(t *testing.T) {
	a := newTestApp(nil)
	out := captureStderr(t, func() {
		feedReads(a, &inputDecoder{}, pasted("hi"), "é", pasted(strings.Repeat("w", scrcpyClipboardMax+1)), "x")
	})
	if out != "" {
		t.Errorf("without control a paste said %q", out)
	}

	n := newTestApp(&controller{})
	captureStderr(t, func() { feedReads(n, &inputDecoder{}, pasted("hi"), "é") })
}

// panicConn is a control socket whose writes panic, as a bug further down
// would.
type panicConn struct{ net.Conn }

func (panicConn) Write([]byte) (int, error) { panic("write failed") }

// TestPastedTextIsNeverPrinted: a paste can hold a password. Neither the debug
// log of raw input and control messages (SCT_DEBUG_CONTROL), whether the paste
// is sent or dropped as too long, nor the notice for input that failed, shows
// its text, raw or in hex.
func TestPastedTextIsNeverPrinted(t *testing.T) {
	const secret = "hunter2-correct-horse"
	hexed := fmt.Sprintf("% x", secret[:8])
	old := dbgControl
	t.Cleanup(func() { dbgControl = old })

	// Typed bytes, then a paste cut inside its text.
	dbgControl = "1"
	a := newTestApp(newController(&capConn{}))
	out := captureTerminal(t, func() { feedReads(a, &inputDecoder{}, "ab"+pasted(secret)[:9], pasted(secret)[9:]+"c") })

	// A paste too long to send: the debug log is on from when it is known to
	// be too long, for the last thousand bytes of it.
	dbgControl = ""
	long := pasted(strings.Repeat(secret, (scrcpyClipboardMax+1000)/len(secret)))
	cut := len("\x1b[200~") + scrcpyClipboardMax + 16
	var d inputDecoder
	left := feedReads(a, &d, readsOf(long[:cut], 4096)...)
	dbgControl = "1"
	out += captureTerminal(t, func() {
		for _, r := range readsOf(long[cut:], 100) {
			left = d.feed(a, append(left, r...), a.handleInput)
		}
	})

	// A paste whose sending panics.
	p := newTestApp(newController(panicConn{}))
	out += captureTerminal(t, func() { feedReads(p, &inputDecoder{}, pasted(secret)) })

	for _, leak := range []string{secret[:8], hexed} {
		if i := strings.Index(out, leak); i >= 0 {
			t.Errorf("the pasted text was printed: ...%q...", out[max(i-80, 0):min(i+80, len(out))])
		}
	}
	for _, want := range []string{"scterm: raw", "set clipboard", "input ignored", "paste not sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("test setup: no %q in what was printed (%d bytes)", want, len(out))
		}
	}
}
