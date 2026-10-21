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

// TestCancelledPasteEndsWhenInputStops: after Ctrl+C cancels a paste, input
// is dropped until the paste's end marker or until none has come for 250 ms,
// counted from the last read; then what was left of it is dropped too, and
// keys act again.
func TestCancelledPasteEndsWhenInputStops(t *testing.T) {
	c := newClockedInput(t)
	out := captureStderr(t, func() {
		c.read("\x1b[200~abc")
		c.after(time.Second)
		c.read("\x03")
		if w := c.wait(); w != 250*time.Millisecond {
			t.Errorf("after the Ctrl+C: the loop waits %v, want 250ms", w)
		}
		c.after(200 * time.Millisecond)
		c.read("q\x1b[20")
		c.after(249 * time.Millisecond)
		if w := c.wait(); w != time.Millisecond {
			t.Errorf("249 ms after the last read: the loop waits %v, want 1ms", w)
		}
		c.after(time.Millisecond)
		if len(c.left) != 0 {
			t.Errorf("250 ms after the last read: % x left of the cancelled paste", c.left)
		}
		c.read("1~x")
	})
	if got := c.sent(); got != "key 8|text ~|key 52" {
		t.Errorf("after the pause: sent %q; want only what was typed after it", got)
	}
	if out != "scterm: paste cancelled by Ctrl+C\n" {
		t.Errorf("said %q", out)
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
// it is dropped as too long to send; or a Ctrl+C, then its end marker or a
// pause), the notice that Ctrl+C cancels an unfinished paste does not come
// back, and the input loop has nothing more to wait for.
func TestNoHintOncePasteIsOver(t *testing.T) {
	const hint = "Ctrl+C cancels the unfinished paste"
	big := strings.Repeat("y", scrcpyClipboardMax+10)
	for _, c := range []struct {
		name  string
		reads []string // a second apart
	}{
		{"ended", []string{"\x1b[200~abc", "def\x1b[201~"}},
		{"too long, whole in one read", []string{pasted(big)}},
		{"too long, then its end marker", []string{"\x1b[200~" + big, "\x1b[201~"}},
		{"cancelled, then a pause", []string{"\x1b[200~abc", "\x03"}},
		{"cancelled, then its end marker", []string{"\x1b[200~abc", "\x03q\x1b[201~"}},
		{"too long, then cancelled", []string{"\x1b[200~" + big, "\x03"}},
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
		if strings.Contains(out, hint) {
			t.Errorf("%s: the notice came after the paste was over: %q", c.name, out)
		}
		if w := ci.wait(); w != 500*time.Millisecond || len(ci.left) != 0 {
			t.Errorf("%s: the loop waits %v for input, with % x left; want 500ms (nothing due) and nothing left", c.name, w, ci.left)
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
// any pauses between them, a Ctrl+C and then a pause leave the decoder reading
// keys again: the key after that is that key, and nothing is left over.
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
			}
			c.read("\x03")
			c.after(time.Second)
			var events []string
			c.emit = func(ev []byte) { events = append(events, string(ev)) }
			c.read("x")
			if len(events) != 1 || events[0] != "x" || len(c.left) != 0 {
				t.Errorf("run %d: after Ctrl+C and a pause, x decoded as %q, left % x", i, events, c.left)
				return
			}
		}
	})
}
