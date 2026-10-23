package main

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// TestInputFuzz drives the real dispatch path with adversarial sequences.
func TestInputFuzz(t *testing.T) {
	a := &app{
		events:  make(chan inputEvent, 64),
		kb:      newKeyboard(),
		grabbed: true,
		audio:   &audioSink{gain: 256, pct: 100},
		tui:     &tui{cols: 80, rows: 39, keys: make([]uint64, 80*39), prev: make([]uint64, 80*39)},
		stream:  &streamState{videoW: 1280, videoH: 720},
	}
	a.stream.updateGeometry()

	seqs := []string{
		"\x1f", "\x1b[27;5;45~", "\x1b[45;5u", "\x1b[1;5-", "\x1b[27;1;45~",
		"\x1b[<0;10;32M", "\x1b[<0;10;32m", "\x1b[<64;1;1M", "\x1b[<65;1;1M",
		"\x1b[<35;10;32M", "\x1b[<0;1;1M", "\x1b[<0;9999;9999M",
		"\x1b[<0;0;0M", "\x1b[<-1;-1;-1M", "\x1b[<99;5;6M",
		"\x1b[?1006h", "\x1b[?1000h", "\x1b[?25l",
		"\x1b[1;2;3;4t", "\x1b[>0;276;0c", "\x1b[6n", "\x1b[2J\x1b[H",
		"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[H", "\x1b[F",
		"\x1b[5~", "\x1b[6~", "\x1b[3~", "\x1b[2~", "\x1b[Z",
		"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", "\x1b[15~", "\x1b[24~",
		"\x1b[1;5A", "\x1b[1;5C", "\x1b[17~",
		"\x1bm", "\x1bM", "\x1b-", "\x1b_", "\x1b=", "\x1b+",
		"\x1bs", "\x1bS", "\x1bk", "\x1bK", // Alt+S screenshot, Alt+K reset-video
		"\x1b", "\x1b[", "\x1b[<", "a", "Z", "0", "9", "\r", "\x7f", "\t",
		"\x1b\x1b", "\x1b\x1b[A",
		"\x1b[38;2;255;255;255m",
		"\x1b[0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0M",
		"\x1b[1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1M",
		"\x1b[<128;10;32M", "\x1b[<31;10;32M", "\x1b[<63;10;32M",
		"\x1b[<0;10;32M\x1b[<0;10;32m",
		// typing beyond ASCII, whole, cut short, or not UTF-8 at all
		"é", "€", "日本", "😀", "e\u0301", "\xc3", "\xc3(", "\xf0\x9f\x98", "\x80", "\xff",
		"\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\x1bé", "\x1b😀", "\x1b\xc3", "\x1b\xff",
		// bracketed paste markers, alone, empty, nested, unterminated, cancelled
		// by a Ctrl+C (fed a byte per read, a second one then ends the
		// dropping after it), after an Esc or an ESC O
		"\x1b[200~", "\x1b[201~", "\x1b[200~\x1b[201~", "\x1b[200~hi\r\n\x1b[201~",
		"\x1b[200~\x1b[200~x\x1b[201~", "\x1b[200~ab\x1bh", "\x1b[200~é\xc3",
		"\x03", "\x1b[200~\x03", "\x1b[200~ab\x03cd", "\x1b[200~a\x03b\x1b[201~x", "\x1b[200~\x03\x1b[201~\x1b[201~",
		"\x1b[200~a\x03b\x03\x1bq\x1b[201~", "\x1b[200~\x03\x03\x1b[20",
		"\x1b\x1b[200~x\x1b[201~", "\x1b\x1b[20", "\x1bO\x1b", "\x1bO\x1b[200~x\x1b[201~", "\x1b\x1b[200~\x03",
	}
	// Through the decoder the input loop uses, which is the one that knows
	// pastes: each in one read and a byte at a time, from a fresh decoder (an
	// unfinished paste would take in all that follows).
	for _, s := range seqs {
		for _, reads := range [][]string{{s}, readsOf(s, 1)} {
			out := captureStderr(t, func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("PANIC on seq %q in %d reads: %v", s, len(reads), r)
					}
				}()
				feedReads(a, &inputDecoder{}, reads...)
			})
			if strings.Contains(out, "input ignored") {
				t.Errorf("seq %q in %d reads: %q", s, len(reads), out)
			}
		}
	}
	for _, s := range seqs {
		bytes := []byte(s)
		// feed through the same chunking as the input loop
		for len(bytes) > 0 {
			ev, n := a.decodeInput(bytes)
			if n == 0 {
				// chunk boundaries: mimic partial reads
				ev, n = a.decodeInput(bytes[:1])
				if n == 0 {
					bytes = bytes[1:]
					continue
				}
			}
			if ev != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("PANIC on seq %q (ev %v): %v", s, ev, r)
							t.FailNow()
						}
					}()
					a.handleInput(ev)
				}()
			}
			bytes = bytes[n:]
		}
	}
}

// TestInputFuzzSplitReads: typed text, chords and pastes go through the
// decoder the input loop uses, in reads cut at random points and a byte at a
// time. Nothing panics, nothing is left over, and what reaches the device is
// the same as when all of it comes in one read. (A CR LF typed outside a
// paste stays out of it: one Enter needs both bytes in one read. So do the
// keys that open overlays or toggle the grab, which write to the terminal.)
func TestInputFuzzSplitReads(t *testing.T) {
	typed := strings.Join([]string{
		"abc Z9!", "é", "ñ€日本😀", "e\u0301", "\xc3(", "\x80\xff\xed\xa0\x80", "\x1bé", "\x1b\x1b",
		"\x1bh", "\x1b[A", "\x1b[1;5C", "\x1b[3~", pasted("one\rtwo\r\nthree"), pasted(""),
		pasted("\x1b[200~nested\x1bh"), pasted("é😀\t"), "\x1b" + pasted("after Esc"), "\x1bO" + pasted("after ESC O"),
		pasted("can\x03celled\x1bq"), "\x03", "tail",
	}, "")
	over := typed + pasted(strings.Repeat("big ", 70000)) + "after" // a paste too long to send

	// Failures are reported with Errorf: a Fatal would end the test with
	// stderr still captured.
	run := func(input string, cuts []int) [][]byte {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		var d inputDecoder
		var pend []byte
		prev := 0
		for _, c := range append(cuts, len(input)) {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("PANIC with reads cut at %v: %v", cuts, r)
					}
				}()
				pend = d.feed(a, append(pend, input[prev:c]...), a.handleInput)
			}()
			prev = c
		}
		if len(pend) != 0 {
			t.Errorf("reads cut at %v left % x", cuts, pend)
		}
		return cc.captured
	}
	same := func(input string, cuts []int, want [][]byte) {
		got := run(input, cuts)
		if !slices.EqualFunc(got, want, bytes.Equal) {
			t.Errorf("reads cut at %v sent %d messages, one read sends %d", cuts, len(got), len(want))
		}
	}

	captureStderr(t, func() {
		rng := rand.New(rand.NewPCG(23, 2004))
		for _, input := range []string{typed, over} {
			want := run(input, nil)
			if len(want) == 0 {
				t.Error("test setup: nothing reached the device")
			}
			for i := 0; i < 40; i++ {
				var cuts []int
				for range rng.IntN(40) + 1 {
					cuts = append(cuts, rng.IntN(len(input)-1)+1)
				}
				slices.Sort(cuts)
				same(input, slices.Compact(cuts), want)
			}
		}
		var each []int
		for i := 1; i < len(typed); i++ {
			each = append(each, i)
		}
		same(typed, each, run(typed, nil))
	})
}
