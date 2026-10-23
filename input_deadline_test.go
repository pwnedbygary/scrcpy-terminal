package main

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The input loop's deadlines, on a clock that moves only when told: how long
// the loop waits for input (inputDecoder.wait), and what it does when none
// came by then (inputDecoder.expire).
// ---------------------------------------------------------------------------

// clockedInput is an app and a decoder on a fake clock, read and expired as
// the input loop does.
type clockedInput struct {
	t    *testing.T
	a    *app
	cc   *capConn
	d    inputDecoder
	now  int64
	left []byte
	emit func([]byte)
}

func newClockedInput(t *testing.T) *clockedInput {
	c := &clockedInput{t: t, cc: &capConn{}, now: int64(time.Hour)}
	c.a = newTestApp(newController(c.cc))
	c.d.now = func() int64 { return c.now }
	c.emit = c.a.handleInput
	return c
}

// read is a read of s.
func (c *clockedInput) read(s string) { c.left = c.d.feed(c.a, append(c.left, s...), c.emit) }

// after lets d go by with no input, and then lets the decoder do what is due.
func (c *clockedInput) after(d time.Duration) {
	c.now += int64(d)
	c.left = c.d.expire(c.a, c.left, c.emit)
}

// wait is how long the input loop would now wait for input.
func (c *clockedInput) wait() time.Duration { return c.d.wait(c.left) }

// idle lets d go by with no input as the input loop does: it waits as long as
// wait says, lets the decoder do what is due, and so on until d has gone by.
// Once what was due is done, the loop must have a moment to wait, or it would
// spin.
func (c *clockedInput) idle(d time.Duration) {
	c.t.Helper()
	for end := c.now + int64(d); c.now < end; {
		c.after(min(c.wait(), time.Duration(end-c.now)))
		if w := c.wait(); w <= 0 {
			c.t.Errorf("once what was due is done, the input loop waits %v, with % x left", w, c.left)
			return
		}
	}
}

// sent is what reached the device since the last call.
func (c *clockedInput) sent() string {
	s := strings.Join(wireLog(c.t, c.cc.captured), "|")
	c.cc.captured = nil
	return s
}

// TestEscWaitsForWhatFollows: a lone Esc is the Esc key once 25 ms have gone
// by with nothing after it; an Esc with what could become a paste's start
// marker after it waits as long, then decodes as before (Alt+Esc, which does
// nothing, and the rest typed); within the 25 ms, the rest of the marker makes
// the Esc the Esc key and the paste one paste.
func TestEscWaitsForWhatFollows(t *testing.T) {
	c := newClockedInput(t)
	c.read("\x1b")
	if w := c.wait(); w != 25*time.Millisecond {
		t.Errorf("a lone Esc: the loop waits %v for input, want 25ms", w)
	}
	c.after(24 * time.Millisecond)
	if got := c.sent(); got != "" || string(c.left) != "\x1b" {
		t.Errorf("24 ms after a lone Esc: sent %q, left % x; want nothing yet", got, c.left)
	}
	c.after(time.Millisecond)
	if got := c.sent(); got != "type 4|type 4" || len(c.left) != 0 {
		t.Errorf("25 ms after a lone Esc: sent %q, left % x; want Back", got, c.left)
	}

	c.read("\x1b\x1b[2")
	if w := c.wait(); w != 25*time.Millisecond {
		t.Errorf("Esc and the start of a start marker: the loop waits %v, want 25ms", w)
	}
	c.after(25 * time.Millisecond)
	if got := c.sent(); got != "text [|key 9" || len(c.left) != 0 {
		t.Errorf("25 ms later: sent %q, left % x; want the [ and the 2 typed", got, c.left)
	}

	captureStderr(t, func() {
		c.read("\x1b\x1b[20")
		c.after(20 * time.Millisecond)
		c.read("0~hi\x1b[201~")
		c.read("\x1b")
		c.after(10 * time.Millisecond)
		c.read("\x1b[200~ho\x1b[201~")
	})
	if got := c.sent(); got != "type 4|type 4|paste hi|type 4|type 4|paste ho" {
		t.Errorf("an Esc and a paste within 25 ms, twice: sent %q", got)
	}

	c.now += int64(time.Second)
	c.read("\x1b")
	c.now += int64(30 * time.Millisecond)
	if w := c.wait(); w != 0 {
		t.Errorf("an Esc whose 25 ms are over: the loop waits %v, want 0", w)
	}
}

// TestWaitWithNothingDue: with nothing due, the loop waits up to 500 ms at a
// time, also for what waits for more input with no time limit, as an escape
// sequence cut short does.
func TestWaitWithNothingDue(t *testing.T) {
	c := newClockedInput(t)
	for _, pend := range []string{"", "\x1b[", "\x1b[1;5", "\x1bO", "\xc3", "\x1b[20"} {
		if w := c.d.wait([]byte(pend)); w != 500*time.Millisecond {
			t.Errorf("left %q: the loop waits %v, want 500ms", pend, w)
		}
	}
}

// TestCancelledPasteIsDroppedToItsEnd: after Ctrl+C cancels a paste, what
// follows is dropped up to the paste's end marker, however late the rest of
// the paste comes, as on a slow link: 300 ms, 2 s or 10 s after the Ctrl+C,
// and its end marker as long after that. Nothing in it acts (not its Alt+Q,
// not its CR), the end marker ends the dropping also when a read cuts it, even
// right after its Esc (all that is then left over, which is not the Esc key
// however long it waits), and the key after it acts as before. What is said
// is the cancel, then every 2 s that keys are dropped, and, once that has been
// said, at the end marker that the paste ended.
func TestCancelledPasteIsDroppedToItsEnd(t *testing.T) {
	const (
		cancelled = "scterm: paste cancelled by Ctrl+C\n"
		dropping  = "scterm: Ctrl+C stops it: keys dropped until the paste ends\n"
		ended     = "scterm: paste ended: keys no longer dropped\n"
	)
	for _, tc := range []struct {
		late     time.Duration
		warnings int // dropping notices in the two waits: one every 2 s
	}{{300 * time.Millisecond, 0}, {2 * time.Second, 2}, {10 * time.Second, 10}} {
		for _, reads := range [][]string{
			{"\x1b[200~ab\x03cd", "ef\x1bq\rgh\x1b[20", "1~x"},
			{"\x1b[200~ab\x03", "\x1b", "[201~x"},
		} {
			c := newClockedInput(t)
			out := captureStderr(t, func() {
				for i, r := range reads {
					if i > 0 {
						c.idle(tc.late)
					}
					c.read(r)
				}
			})
			quit := quitQueued(c.a)
			if got := c.sent(); got != "key 52" || quit || len(c.left) != 0 {
				t.Errorf("reads %q, %v apart: sent %q, quit %v, left % x; want only the x after the end marker",
					reads, tc.late, got, quit, c.left)
			}
			want := cancelled + strings.Repeat(dropping, tc.warnings)
			if tc.warnings > 0 {
				want += ended
			}
			if out != want {
				t.Errorf("reads %q, %v apart: said %q, want %q", reads, tc.late, out, want)
			}
		}
	}
}

// TestDroppingNotice: 2 s after a Ctrl+C cancelled a paste, while what follows
// is still dropped, the status row says so and how to stop it, Ctrl+C first:
// whole in an 80-column terminal (50 cells after the hints), cut short from its
// end in a narrower one, Ctrl+C still there in 40 columns. It comes however
// many keys come in the meantime (they are dropped too), and again every 2 s
// with no input at all, before the 4 s a notice stays up run out. When the
// paste's end marker comes, a notice that the paste ended replaces it, so that
// it no longer says to press Ctrl+C, and nothing comes back after that; when
// Ctrl+C pressed again stops the dropping, the status row says so instead. An
// end marker that comes before the first such notice says nothing more. None
// is kept for the replay at exit.
func TestDroppingNotice(t *testing.T) {
	const dropping = "Ctrl+C stops it: keys dropped until the paste ends"
	startNotices(t)
	c := newClockedInput(t)
	c.read("\x1b[200~abc")
	c.idle(3 * time.Second)
	c.read("\x03")
	c.idle(1500 * time.Millisecond)
	c.read("x")
	c.idle(499 * time.Millisecond)
	if n := notices.current(timeNowUnixNano()); n != "paste cancelled by Ctrl+C" {
		t.Errorf("1.999 s after the Ctrl+C the status row shows %q", n)
	}
	if w := c.wait(); w != time.Millisecond {
		t.Errorf("1.999 s after the Ctrl+C the loop waits %v, want 1ms", w)
	}
	c.after(time.Millisecond)
	if n := notices.current(timeNowUnixNano()); n != dropping {
		t.Errorf("2 s after the Ctrl+C the status row shows %q, want %q", n, dropping)
	}
	for _, w := range []struct {
		cols int
		want string // after the hints
	}{{80, dropping}, {60, "Ctrl+C stops it: keys dropped…"}, {40, "Ctrl+C st…"}} {
		if got, want := statusApp(w.cols, "Pixel 8").statusLine(), statusHintsText+" | "+w.want; got != want {
			t.Errorf("%d columns:\n got %q\nwant %q", w.cols, got, want)
		}
	}
	for i := 1; i <= 2; i++ {
		ageNotice(5 * time.Second) // the notice has gone
		c.idle(1999 * time.Millisecond)
		if n := notices.current(timeNowUnixNano()); n != "" {
			t.Errorf("1.999 s after notice %d the status row shows %q", i, n)
		}
		c.after(time.Millisecond)
		if n := notices.current(timeNowUnixNano()); n != dropping {
			t.Errorf("2 s after notice %d the status row shows %q, want %q", i, n, dropping)
		}
	}
	c.read("y\x1b[201~z")
	if n := notices.current(timeNowUnixNano()); n != "paste ended: keys no longer dropped" {
		t.Errorf("at the paste's end marker the status row shows %q", n)
	}
	ageNotice(5 * time.Second)
	c.idle(time.Hour)
	if n := notices.current(timeNowUnixNano()); n != "" {
		t.Errorf("after the paste's end marker, and an hour, the status row shows %q", n)
	}

	c.read("\x1b[200~ab\x03cd")
	c.idle(2 * time.Second)
	if n := notices.current(timeNowUnixNano()); n != dropping {
		t.Errorf("2 s after a second cancel the status row shows %q, want %q", n, dropping)
	}
	c.read("\x03")
	if n := notices.current(timeNowUnixNano()); n != "keys no longer dropped" {
		t.Errorf("after Ctrl+C again the status row shows %q", n)
	}
	ageNotice(5 * time.Second)
	c.idle(time.Hour)
	if n := notices.current(timeNowUnixNano()); n != "" {
		t.Errorf("an hour after Ctrl+C again the status row shows %q", n)
	}

	c.read("\x1b[200~ab\x03cd")
	c.idle(1999 * time.Millisecond)
	c.read("ef\x1b[201~")
	if n := notices.current(timeNowUnixNano()); n != "paste cancelled by Ctrl+C" {
		t.Errorf("at an end marker 1.999 s after a third cancel the status row shows %q", n)
	}
	if got := c.sent(); got != "key 54" {
		t.Errorf("sent %q, want only the z after the first paste's end marker", got)
	}
	var replay strings.Builder
	notices.stop(&replay)
	if replay.Len() != 0 {
		t.Errorf("the replay at exit is %q, want nothing", replay.String())
	}
}

// TestCtrlCAgainStopsTheDropping: where a cancelled paste's end marker never
// comes, Ctrl+C pressed again ends the dropping at once: a read of nothing but
// Ctrl+C, once, twice or 4 times, also after keys, or after what could begin
// the end marker (which goes too), and also where the paste was too long to
// send. That Ctrl+C is not sent, a notice says that keys are no longer
// dropped, nothing more is due, and the keys after it act, a Ctrl+C too. A
// Ctrl+C that comes with other bytes in its read, as a pasted one does, or in
// the read that cancelled, does not end it, nor do 5 in one read (a paste's run
// of them can fill a read), nor does an empty read: a key a second later is
// still dropped.
func TestCtrlCAgainStopsTheDropping(t *testing.T) {
	const (
		cancelled = "scterm: paste cancelled by Ctrl+C\n"
		stopped   = "scterm: keys no longer dropped\n"
	)
	for _, c := range []struct {
		name  string
		paste string   // the first read: a paste, cut short by its Ctrl+C
		reads []string // the reads after it, 300 ms apart
		stops bool
	}{
		{"Ctrl+C", "\x1b[200~ab\x03cd", []string{"\x03"}, true},
		{"Ctrl+C twice in one read", "\x1b[200~ab\x03cd", []string{"\x03\x03"}, true},
		{"Ctrl+C 4 times in one read", "\x1b[200~ab\x03cd", []string{"\x03\x03\x03\x03"}, true},
		{"Ctrl+C after keys", "\x1b[200~ab\x03cd", []string{"q\r", "\x03"}, true},
		{"Ctrl+C after the start of an end marker", "\x1b[200~ab\x03cd", []string{"ef\x1b[20", "\x03"}, true},
		{"Ctrl+C after cancelling a paste too long to send", "\x1b[200~" + strings.Repeat("y", maxPasteBytes+10), []string{"\x03", "\x03"}, true},
		{"Ctrl+C, then a key, in one read", "\x1b[200~ab\x03cd", []string{"\x03x"}, false},
		{"a key, then Ctrl+C, in one read", "\x1b[200~ab\x03cd", []string{"x\x03"}, false},
		{"Ctrl+C in more of the paste", "\x1b[200~ab\x03cd", []string{"ef\x03gh"}, false},
		{"Ctrl+C in the read that cancelled", "\x1b[200~ab\x03\x03", nil, false},
		{"Ctrl+C 5 times in one read", "\x1b[200~ab\x03cd", []string{"\x03\x03\x03\x03\x03"}, false},
		{"an empty read", "\x1b[200~ab\x03cd", []string{""}, false},
	} {
		ci := newClockedInput(t)
		var due bool
		out := captureStderr(t, func() {
			ci.read(c.paste)
			for _, r := range c.reads {
				ci.idle(300 * time.Millisecond)
				ci.read(r)
			}
			if c.stops {
				_, due = ci.d.deadline(ci.left)
				ci.read("x")
				ci.read("\x03")
			} else {
				ci.idle(time.Second)
				ci.read("x")
			}
		})
		got := ci.sent()
		switch {
		case c.stops && (got != "key 52|key 31 meta 0x1000" || out != cancelled+stopped || due || len(ci.left) != 0):
			t.Errorf("%s: sent %q, said %q, then something due %v, with % x left; want the x and a Ctrl+C after it, %q, and nothing due",
				c.name, got, out, due, ci.left, cancelled+stopped)
		case !c.stops && (got != "" || out != cancelled):
			t.Errorf("%s: sent %q, said %q; want the x dropped too, and %q", c.name, got, out, cancelled)
		}
	}
}

// TestUnfinishedPasteHint: a paste unfinished for 2 s, counted from its start
// however much more of it comes in, gets a notice that Ctrl+C cancels it. The
// notice comes back every 2 s with no input at all, before the 4 s a notice
// stays up run out, for as long as the paste stays unfinished. It is not kept
// for the replay at exit, and once the paste ends it does not come back.
func TestUnfinishedPasteHint(t *testing.T) {
	const hint = "Ctrl+C cancels the unfinished paste"
	startNotices(t)
	c := newClockedInput(t)
	c.read("\x1b[200~abc")
	if w := c.wait(); w != 500*time.Millisecond {
		t.Errorf("an unfinished paste: the loop waits %v, want 500ms (at most)", w)
	}
	c.after(1500 * time.Millisecond)
	c.read("d")
	c.after(499 * time.Millisecond)
	if n := notices.current(timeNowUnixNano()); n != "" {
		t.Errorf("after 1.999 s the status row shows %q", n)
	}
	if w := c.wait(); w != time.Millisecond {
		t.Errorf("after 1.999 s the loop waits %v, want 1ms", w)
	}
	c.after(time.Millisecond)
	if n := notices.current(timeNowUnixNano()); n != hint {
		t.Errorf("after 2 s the status row shows %q, want %q", n, hint)
	}
	for i := 1; i <= 2; i++ {
		ageNotice(5 * time.Second) // the notice has gone
		c.after(1999 * time.Millisecond)
		if n := notices.current(timeNowUnixNano()); n != "" {
			t.Errorf("1.999 s after notice %d the status row shows %q", i, n)
		}
		c.after(time.Millisecond)
		if n := notices.current(timeNowUnixNano()); n != hint {
			t.Errorf("2 s after notice %d the status row shows %q, want %q", i, n, hint)
		}
	}
	c.read("ef\x1b[201~")
	c.after(time.Hour)
	if n := notices.current(timeNowUnixNano()); n != "pasted 6 characters" {
		t.Errorf("after the paste ended, and an hour, the status row shows %q", n)
	}
	if got := c.sent(); got != "paste abcdef" {
		t.Errorf("sent %q", got)
	}
	var replay strings.Builder
	notices.stop(&replay)
	if replay.Len() != 0 {
		t.Errorf("the replay at exit is %q, want nothing", replay.String())
	}
}

// TestNoHintOncePasteIsOver: however a paste ends (its end marker, also once
// it is dropped as too long to send; or a Ctrl+C, then its end marker or
// Ctrl+C again), neither notice on Ctrl+C (that it cancels an unfinished
// paste, or that it stops the dropping after a cancelled one) comes back, and
// the input loop has nothing more to wait for.
func TestNoHintOncePasteIsOver(t *testing.T) {
	const hint = "Ctrl+C cancels the unfinished paste"
	const dropping = "keys dropped until the paste ends"
	big := strings.Repeat("y", scrcpyClipboardMax+10)
	for _, c := range []struct {
		name  string
		reads []string // a second apart
	}{
		{"ended", []string{"\x1b[200~abc", "def\x1b[201~"}},
		{"too long, whole in one read", []string{pasted(big)}},
		{"too long, then its end marker", []string{"\x1b[200~" + big, "\x1b[201~"}},
		{"cancelled, then Ctrl+C again", []string{"\x1b[200~abc", "\x03", "\x03"}},
		{"cancelled, then its end marker", []string{"\x1b[200~abc", "\x03q\x1b[201~"}},
		{"too long, then cancelled, then Ctrl+C again", []string{"\x1b[200~" + big, "\x03", "\x03"}},
	} {
		ci := newClockedInput(t)
		out := captureStderr(t, func() {
			for _, r := range c.reads {
				ci.read(r)
				ci.after(time.Second)
			}
			for range 5 {
				ci.after(3 * time.Second)
			}
		})
		if strings.Contains(out, hint) || strings.Contains(out, dropping) {
			t.Errorf("%s: a notice came after the paste was over: %q", c.name, out)
		}
		if at, due := ci.d.deadline(ci.left); due || len(ci.left) != 0 {
			t.Errorf("%s: the loop has a deadline (%v, at %d), with % x left; want nothing due and nothing left", c.name, due, at, ci.left)
		}
	}
}

// TestInputClockIsMonotonic: the decoder's real clock is the time since a
// reading of the monotonic clock, which goes on as time does whatever the
// system time is set to (by hand, or by NTP stepping it), so that setting it
// moves no deadline: an Esc is still the Esc key 25 ms after it came, not an
// Alt chord with the next key. A test cannot set the system time; Round(0)
// takes a time's monotonic reading away.
func TestInputClockIsMonotonic(t *testing.T) {
	if inputClockStart == inputClockStart.Round(0) {
		t.Errorf("the input clock starts at %v, which has no monotonic reading", inputClockStart)
	}
	var d inputDecoder
	before := time.Since(inputClockStart)
	got := time.Duration(d.clock())
	after := time.Since(inputClockStart)
	if got < before || got > after {
		t.Errorf("the input clock reads %v; want the time since it started, between %v and %v", got, before, after)
	}
}

// TestCtrlCAlwaysGetsOut: whatever came before (keys, sequences cut short,
// pastes finished or not, too long, cancelled), in reads cut anywhere and with
// any pauses between them, a Ctrl+C, a pause, Ctrl+C again (each a read of its
// own, as a key press is) and a pause leave the decoder reading keys again:
// the key after that is that key, and nothing is left over. Along the way,
// once what was due is done, the input loop always has a moment to wait: it
// never spins.
func TestCtrlCAlwaysGetsOut(t *testing.T) {
	tokens := []string{"a", "\r", "\t", "\x07", "\x1b", "\x1b[", "\x1b[1;", "\x1bO", "\x1b\x1b", "\x1b[200~",
		"\x1b[201~", "\x1b[20", "\x1b[2", "\x03", "é", "\xc3", "\x1b\xc3", "hi", "\x1b[A", "\x1bq", "\x1b[<0;5;5M"}
	big := strings.Repeat("y", maxPasteBytes+10)
	pauses := []time.Duration{0, time.Millisecond, 24 * time.Millisecond, 25 * time.Millisecond,
		249 * time.Millisecond, 250 * time.Millisecond, 2 * time.Second}
	rng := rand.New(rand.NewPCG(27, 3))
	captureTerminal(t, func() {
		for i := 0; i < 3000; i++ {
			var b strings.Builder
			for range rng.IntN(12) + 1 {
				if rng.IntN(100) == 0 {
					b.WriteString(big)
				} else {
					b.WriteString(tokens[rng.IntN(len(tokens))])
				}
			}
			input := b.String()
			c := newClockedInput(t)
			c.emit = func([]byte) {}
			for len(input) > 0 {
				n := min(rng.IntN(8)+1, len(input))
				if len(input) > 1000 && rng.IntN(4) != 0 {
					n = min(rng.IntN(70000)+1000, len(input))
				}
				c.read(input[:n])
				input = input[n:]
				c.after(pauses[rng.IntN(len(pauses))])
				if w := c.wait(); w <= 0 {
					t.Errorf("run %d: once what was due is done, the input loop waits %v, with % x left", i, w, c.left)
					return
				}
			}
			c.read("\x03")
			c.after(time.Second)
			c.read("\x03")
			c.after(time.Second)
			var events []string
			c.emit = func(ev []byte) { events = append(events, string(ev)) }
			c.read("x")
			if len(events) != 1 || events[0] != "x" || len(c.left) != 0 {
				t.Errorf("run %d: after Ctrl+C, a pause, Ctrl+C and a pause, x decoded as %q, left % x", i, events, c.left)
				return
			}
		}
	})
}
