package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
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
		s, _ := peerWith(t, c.grants, c.control, peer.LeaseHeld)
		a.sess = &session{peer: s, deviceName: "Pixel 8"}
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

	// A paste with a Ctrl+C in it, and more of it after.
	c := newTestApp(newController(&capConn{}))
	out += captureTerminal(t, func() { feedReads(c, &inputDecoder{}, "\x1b[200~x\x03"+secret, secret+"\x1b[201~") })

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

// ---------------------------------------------------------------------------
// Getting out of a paste, an Esc just before one, and what a paste says.
// ---------------------------------------------------------------------------

// peerWith connects to a fake paired target as --peer does, through
// peer.Client.Connect, and starts the session. The target's welcome grants
// grants, lists control as its backend's control messages (nil: it lists no
// capabilities at all) and gives this computer the input lease state lease
// (docs/PEER_PROTOCOL.md). moveLease sends the lease event a target sends when
// the lease moves, and returns once the session has read it.
func peerWith(t *testing.T, grants, control []string, lease string) (s *peer.Session, moveLease func(state, holder string)) {
	t.Helper()
	target, err := peer.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{target.Certificate}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	welcome := peer.Message{Type: peer.TypeWelcome, V: 1, Channel: peer.ChannelControl, Session: "s1",
		Token: strings.Repeat("0", 64), Grants: grants, Lease: lease, Device: &peer.DeviceInfo{Name: "Pixel 8"}}
	if control != nil {
		welcome.Caps = &peer.Capabilities{Control: control}
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		defer conn.Close()
		if _, err := peer.ReadFrame(conn); err != nil || peer.WriteFrame(conn, &welcome) != nil {
			close(accepted)
			return
		}
		accepted <- conn
		io.Copy(io.Discard, conn) // pings, and the bye
	}()
	me, err := peer.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &peer.Client{Identity: me, Info: peer.ClientInfo{Name: "scterm test", App: "scterm/test"}}
	s, err = client.Connect("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, target.Fingerprint, peer.StreamRequest{Control: true})
	if err != nil {
		t.Fatal(err)
	}
	leases := make(chan string, 8)
	s.Start(peer.Events{Lease: func(state, _ string) { leases <- state }})
	t.Cleanup(func() { s.Close(false) })
	conn, ok := <-accepted
	if !ok {
		t.Fatal("the fake target did not welcome the session")
	}
	return s, func(state, holder string) {
		t.Helper()
		// An envelope: the byte 0xFE, then a frame.
		if _, err := conn.Write([]byte{0xfe}); err != nil {
			t.Fatal(err)
		}
		if err := peer.WriteFrame(conn, &peer.Message{Type: peer.TypeLease, State: state, Holder: holder}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-leases:
		case <-time.After(5 * time.Second):
			t.Fatal("the session did not read the lease event")
		}
	}
}

// TestCtrlCCancelsAPaste: raw mode reads Ctrl+C as a byte, and inside a paste
// it cancels it: the way out of a paste whose end marker never comes, inside
// which the keys that would quit or close an overlay are only more of the
// paste. Nothing of it is sent, a notice says it was cancelled, and what
// follows is dropped up to the paste's end marker, however late that comes
// (TestCancelledPasteIsDroppedToItsEnd), so keys typed at once act as nothing;
// after it, keys act again. A paste that holds a Ctrl+C character, or two, is
// dropped whole the same way, never typed, however two reads cut it, and also
// once it is too long to send; so is one holding a run of them that fills
// reads of its own. A Ctrl+C after a paste's end marker, even in the same
// read, is not in it: it is the key.
func TestCtrlCCancelsAPaste(t *testing.T) {
	const cancelled = "scterm: paste cancelled by Ctrl+C\n"

	cc := &capConn{}
	a := newTestApp(newController(cc))
	var d inputDecoder
	var left []byte
	out := captureStderr(t, func() { left = feedReads(a, &d, "\x1b[200~abc", "\x03", "\x1bq\r") })
	quit := quitQueued(a)
	if len(cc.captured) != 0 || quit || out != cancelled {
		t.Errorf("Ctrl+C in an unfinished paste, then Alt+Q and Enter: sent %d messages, quit %v, said %q; want nothing sent and %q",
			len(cc.captured), quit, out, cancelled)
	}
	out = captureStderr(t, func() {
		for _, r := range []string{"de\x1b[20", "1~", "x"} {
			left = d.feed(a, append(left, r...), a.handleInput)
		}
	})
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "key 52" || len(left) != 0 || out != "" {
		t.Errorf("then the paste's end marker and x: sent %q, left % x, said %q; want only the x", got, left, out)
	}

	for _, p := range []string{pasted("a\x03b\rc\x1bq") + "x", pasted("a\x03b\rc\x03d\x1bq\re") + "x"} {
		for i := 0; i < len(p); i++ {
			cc := &capConn{}
			a := newTestApp(newController(cc))
			var left []byte
			out := captureStderr(t, func() { left = feedReads(a, &inputDecoder{}, p[:i], p[i:]) })
			quit := quitQueued(a)
			if got := wireLog(t, cc.captured); strings.Join(got, "|") != "key 52" || len(left) != 0 || quit || out != cancelled {
				t.Errorf("%q, cut after %d bytes: sent %q, left % x, quit %v, said %q; want only the x, and %q",
					p, i, got, left, quit, out, cancelled)
			}
		}
	}

	long := pasted(strings.Repeat("y", scrcpyClipboardMax+100)+"\x03zz") + "x"
	for _, reads := range [][]string{{long}, readsOf(long, 4096)} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var left []byte
		out := captureStderr(t, func() { left = feedReads(a, &inputDecoder{}, reads...) })
		if got := wireLog(t, cc.captured); strings.Join(got, "|") != "key 52" || len(left) != 0 || out != cancelled {
			t.Errorf("a paste too long to send with a Ctrl+C after the limit, in %d reads: sent %q, left % x, said %q; want only the x, and %q",
				len(reads), got, left, out, cancelled)
		}
	}

	run := pasted(strings.Repeat("\x03", 10000)+"z\x1bq") + "x"
	for _, reads := range [][]string{{run}, readsOf(run, 4096)} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var left []byte
		out := captureStderr(t, func() { left = feedReads(a, &inputDecoder{}, reads...) })
		quit := quitQueued(a)
		if got := wireLog(t, cc.captured); len(got) != 1 || got[0] != "key 52" || len(left) != 0 || quit || out != cancelled {
			t.Errorf("a paste holding 10000 Ctrl+C characters, in %d reads: sent %d messages, left % x, quit %v, said %q; want only the x, and %q",
				len(reads), len(got), left, quit, out, cancelled)
		}
	}

	over := pasted(strings.Repeat("y", scrcpyClipboardMax+100)) + "\x03x"
	for _, c := range []struct {
		name  string
		reads []string
		want  string
	}{
		{"a paste", []string{pasted("hi") + "\x03x"}, "paste hi|key 31 meta 0x1000|key 52"},
		{"a paste too long to send", []string{over}, "key 31 meta 0x1000|key 52"},
		{"a paste too long to send, in 4096-byte reads", readsOf(over, 4096), "key 31 meta 0x1000|key 52"},
	} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var left []byte
		out := captureStderr(t, func() { left = feedReads(a, &inputDecoder{}, c.reads...) })
		if got := strings.Join(wireLog(t, cc.captured), "|"); got != c.want || len(left) != 0 || strings.Contains(out, "cancelled") {
			t.Errorf("%s, then Ctrl+C and x in the read with its end marker: sent %q, left % x, said %q; want %q, nothing cancelled",
				c.name, got, left, out, c.want)
		}
	}
}

// TestEscBeforeAPaste: an Esc typed just before a paste, in the read that
// brings its start marker or in the one before (the input loop holds a lone
// Esc 25 ms for what follows it), is the Esc key, Back, and the paste one
// paste. Read together with the start marker's Esc as Alt+Esc, it used to
// leave the rest of the marker and the whole paste typed key by key, so that a
// pasted Alt+Q quit and a CR pressed Enter. An ESC O waits for its third byte
// however long, and an Esc is not that byte either: ESC O is then Alt+O, which
// does nothing. The same however the reads cut it.
func TestEscBeforeAPaste(t *testing.T) {
	const paste = "\x1b[200~hi\rx\x1bq\x1b[201~"
	for _, c := range []struct{ name, input, want string }{
		{"Esc", "\x1b" + paste + "z", "type 4|type 4|paste hi\nxq|key 54"},
		{"ESC O", "\x1bO" + paste + "z", "paste hi\nxq|key 54"},
	} {
		for i := 0; i < len(c.input); i++ {
			cc := &capConn{}
			a := newTestApp(newController(cc))
			var left []byte
			captureStderr(t, func() { left = feedReads(a, &inputDecoder{}, c.input[:i], c.input[i:]) })
			quit := quitQueued(a)
			if got := strings.Join(wireLog(t, cc.captured), "|"); got != c.want || len(left) != 0 || quit {
				t.Errorf("%s then a paste, cut after %d bytes: sent %q, left % x, quit %v; want %q", c.name, i, got, left, quit, c.want)
			}
		}
	}
}

// TestPasteNeedsTheLease: a peer takes input only from the device holding its
// input lease, and refuses the rest with an error that this computer reports
// once per run (docs/PEER_PROTOCOL.md; peermode.go). So a paste is sent only
// while this computer holds the lease, and then says so; without the lease
// nothing is sent, and the notice says why, each time: also after the lease
// moved to another device during the session, and where the paste would have
// been typed.
func TestPasteNeedsTheLease(t *testing.T) {
	const refused = "scterm: paste not sent: no control (--takeover takes it)\n"
	grants := []string{"view", "control", "clipboard"}
	control := []string{"inject_text", "set_clipboard"}

	s, moveLease := peerWith(t, grants, control, peer.LeaseHeld)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.sess = &session{peer: s, deviceName: "Pixel 8"}
	out := captureStderr(t, func() { feedReads(a, &inputDecoder{}, pasted("hi")) })
	if got := onePaste(t, cc.captured); got != "hi" || out != "scterm: pasted 2 characters\n" {
		t.Errorf("holding the lease: pasted %q, said %q", got, out)
	}

	moveLease(peer.LeaseViewer, "Galaxy Tab")
	cc.captured = nil
	out = captureStderr(t, func() { feedReads(a, &inputDecoder{}, pasted("one"), pasted("two")) })
	if len(cc.captured) != 0 || out != refused+refused {
		t.Errorf("two pastes after the lease moved away: sent %d messages, said %q; want nothing sent and, twice, %q",
			len(cc.captured), out, refused)
	}

	for _, g := range [][]string{grants, {"view", "control"}} {
		v, _ := peerWith(t, g, control, peer.LeaseViewer)
		vc := &capConn{}
		b := newTestApp(newController(vc))
		b.sess = &session{peer: v, deviceName: "Pixel 8"}
		out := captureStderr(t, func() { feedReads(b, &inputDecoder{}, pasted("hi")) })
		if len(vc.captured) != 0 || out != refused {
			t.Errorf("granted %v, viewing from the start: sent %d messages, said %q; want nothing sent and %q", g, len(vc.captured), out, refused)
		}
	}
}

// TestTypedPasteLimit: where the device clipboard cannot take a paste (here the
// pairing does not grant it), the device types it key by key, pressing Enter
// at each line break, and nothing stops it once it is sent. So it is limited
// to 4096 bytes of the text as typed (line breaks folded to LF, control
// characters out): a longer paste is refused whole, never cut, with a notice
// giving its size and the limit. With the device clipboard the same paste goes
// as one.
func TestTypedPasteLimit(t *testing.T) {
	const limit = 4096
	control := []string{"inject_text", "set_clipboard"}
	typedOnly, _ := peerWith(t, []string{"view", "control"}, control, peer.LeaseHeld)
	withClipboard, _ := peerWith(t, []string{"view", "control", "clipboard"}, control, peer.LeaseHeld)
	most := strings.Repeat("é", limit/2-1) + "ab"
	folded := strings.Repeat("abc\r\n", limit/4) // 5120 bytes pasted, 4096 typed
	over := most + "\x01c"
	for _, c := range []struct{ name, raw, typed string }{
		{"4096 bytes", most, most},
		{"4096 bytes once CR LF is folded", folded, strings.ReplaceAll(folded, "\r\n", "\n")},
		{"4097 bytes", over, ""},
	} {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		a.sess = &session{peer: typedOnly, deviceName: "Pixel 8"}
		out := captureStderr(t, func() { feedReads(a, &inputDecoder{}, readsOf(pasted(c.raw), 4096)...) })
		var typed strings.Builder
		for i, w := range cc.captured {
			if w[0] != ctrlInjectText {
				t.Fatalf("%s: message %d is not injected text: % x", c.name, i, w[:min(len(w), 16)])
			}
			typed.Write(w[5:])
		}
		if c.typed != "" {
			if typed.String() != c.typed || !strings.HasPrefix(out, "scterm: pasted ") {
				t.Errorf("%s: typed %d bytes, said %q; want the %d bytes typed", c.name, typed.Len(), out, len(c.typed))
			}
			continue
		}
		want := fmt.Sprintf("scterm: paste not sent: %d bytes, at most %d typed\n", limit+1, limit)
		if len(cc.captured) != 0 || out != want {
			t.Errorf("%s: sent %d messages, said %q; want nothing sent and %q", c.name, len(cc.captured), out, want)
		}
	}

	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.sess = &session{peer: withClipboard, deviceName: "Pixel 8"}
	captureStderr(t, func() { feedReads(a, &inputDecoder{}, pasted(over)) })
	if got := onePaste(t, cc.captured); got != most+"c" {
		t.Errorf("with the device clipboard, the %d-byte paste arrived as %d bytes", len(most)+1, len(got))
	}
}

// TestPasteNoticesAreNotReplayed: the notices about a paste show in the status
// row, but are not printed again when the terminal is given back, where they
// would leave a paste's length (a password's, say) in the shell's scrollback:
// that it was sent and how long it was, or that it was too long, cancelled or
// refused. Other notices still are, and so is the error for a paste that
// failed, without the paste's length.
func TestPasteNoticesAreNotReplayed(t *testing.T) {
	startNotices(t)
	control := []string{"inject_text", "set_clipboard"}
	viewing, _ := peerWith(t, []string{"view", "control", "clipboard"}, control, peer.LeaseViewer)
	typedOnly, _ := peerWith(t, []string{"view", "control"}, control, peer.LeaseHeld)
	for _, c := range []struct {
		name  string
		sess  *session
		reads []string
		shows string
	}{
		{"sent", nil, []string{pasted("hunter2")}, "pasted 7 characters"},
		{"too long", nil, readsOf(pasted(strings.Repeat("w", scrcpyClipboardMax+1)), 4096), fmt.Sprintf("paste not sent: %d bytes", scrcpyClipboardMax+1)},
		{"cancelled", nil, []string{pasted("a\x03b")}, "paste cancelled by Ctrl+C"},
		{"without the lease", &session{peer: viewing, deviceName: "Pixel 8"}, []string{pasted("hi")}, "paste not sent: no control"},
		{"too long to type", &session{peer: typedOnly, deviceName: "Pixel 8"}, []string{pasted(strings.Repeat("t", 5000))}, "paste not sent: 5000 bytes"},
	} {
		a := newTestApp(newController(&capConn{}))
		a.sess = c.sess
		feedReads(a, &inputDecoder{}, c.reads...)
		if got := notices.current(timeNowUnixNano()); !strings.HasPrefix(got, c.shows) {
			t.Errorf("%s: the status row shows %q, want %q", c.name, got, c.shows)
		}
	}
	feedReads(newTestApp(newController(panicConn{})), &inputDecoder{}, pasted("hunter2"))
	noticef("scterm: saved scterm-5.ppm (80x46)\n")
	var replay strings.Builder
	notices.stop(&replay)
	if got, want := replay.String(), "scterm: input ignored: write failed (a paste)\nscterm: saved scterm-5.ppm (80x46)\n"; got != want {
		t.Errorf("the replay at exit is %q, want %q", got, want)
	}
}

// TestPasteRefusalsFitTheStatusRow: in an 80-column terminal the status row
// has 50 cells after its hints for a notice. The notice that a paste was not
// sent without the lease, or as too long to type (as long as a paste can be),
// fits there whole, whatever the device's name.
func TestPasteRefusalsFitTheStatusRow(t *testing.T) {
	startNotices(t)
	control := []string{"inject_text", "set_clipboard"}
	viewing, _ := peerWith(t, []string{"view", "control", "clipboard"}, control, peer.LeaseViewer)
	typedOnly, _ := peerWith(t, []string{"view", "control"}, control, peer.LeaseHeld)
	const device = "Galaxy Tab S9 Ultra in the living room"
	for _, c := range []struct {
		name  string
		sess  *session
		text  string
		shows string
	}{
		{"without the lease", &session{peer: viewing, deviceName: device}, "hi", "paste not sent: no control (--takeover takes it)"},
		{"too long to type", &session{peer: typedOnly, deviceName: device}, strings.Repeat("t", scrcpyClipboardMax),
			fmt.Sprintf("paste not sent: %d bytes, at most 4096 typed", scrcpyClipboardMax)},
	} {
		a := statusApp(80, device)
		a.ctrl = newController(&capConn{})
		a.sess = c.sess
		feedReads(a, &inputDecoder{}, readsOf(pasted(c.text), 4096)...)
		if got, want := a.statusLine(), statusHintsText+" | "+c.shows; got != want {
			t.Errorf("%s, 80 columns:\n got %q\nwant %q", c.name, got, want)
		}
	}
}

// TestPasteAtTheLimitWithItsEndMarkerCut: a paste up to scrcpy's limit is sent
// whole also when a read ends inside its end marker: the bytes after its text
// may be that marker's first ones, and do not count towards the limit.
func TestPasteAtTheLimitWithItsEndMarkerCut(t *testing.T) {
	for _, n := range []int{scrcpyClipboardMax, scrcpyClipboardMax - 1, scrcpyClipboardMax - 5} {
		text := strings.Repeat("z", n)
		input := pasted(text) + "x"
		marker := len("\x1b[200~") + n
		for k := 1; k < len("\x1b[201~"); k++ {
			cc := &capConn{}
			a := newTestApp(newController(cc))
			var left []byte
			captureStderr(t, func() {
				left = feedReads(a, &inputDecoder{}, append(readsOf(input[:marker+k], 4096), input[marker+k:])...)
			})
			if got := wireLog(t, cc.captured); len(got) != 2 || got[0] != "paste "+text || got[1] != "key 52" || len(left) != 0 {
				t.Errorf("%d bytes, a read ending %d bytes into the end marker: sent %d messages (first: % x), left % x; want the paste, then the x",
					n, k, len(cc.captured), first(cc.captured), left)
			}
		}
	}
}

// TestPasteWithoutTheBackendsCapabilities: a peer whose welcome lists no
// capabilities cannot be asked to set its clipboard, even where its pairing
// grants it, so the paste is typed; nothing fails on the missing list.
func TestPasteWithoutTheBackendsCapabilities(t *testing.T) {
	s, _ := peerWith(t, []string{"view", "control", "clipboard"}, nil, peer.LeaseHeld)
	if s.Welcome.Caps != nil {
		t.Fatal("test setup: the welcome has capabilities")
	}
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.sess = &session{peer: s, deviceName: "Pixel 8"}
	out := captureStderr(t, func() { feedReads(a, &inputDecoder{}, pasted("hi")) })
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "text hi" || out != "scterm: pasted 2 characters\n" {
		t.Errorf("a peer that lists no capabilities: sent %q, said %q; want the text typed", got, out)
	}
}

// TestEmptyPasteSendsNothing: a paste with no text, or none left once control
// characters and bytes that are not UTF-8 are taken out, sends nothing and
// says nothing, and the key after it is typed.
func TestEmptyPasteSendsNothing(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	out := captureStderr(t, func() { feedReads(a, &inputDecoder{}, pasted(""), pasted("\x01\x1b\x7f\x00\x08\xff\xfe"), "x") })
	if got := wireLog(t, cc.captured); strings.Join(got, "|") != "key 52" || out != "" {
		t.Errorf("two empty pastes, then x: sent %q, said %q; want only the x", got, out)
	}
}
