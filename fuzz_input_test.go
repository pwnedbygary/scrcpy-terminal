package main

import (
	"testing"
)

// TestInputFuzz drives the real dispatch path with adversarial sequences.
func TestInputFuzz(t *testing.T) {
	a := &app{
		events:      make(chan inputEvent, 64),
		paletteKeys: []int{3, 4, 82, 187, 24, 25, 91},
		grabbed:     true,
		audio:       &audioSink{gain: 256},
		tui:         &tui{cols: 80, rows: 39, keys: make([]uint64, 80*39), prev: make([]uint64, 80*39)},
		stream:      &streamState{videoW: 1280, videoH: 720},
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
		"\x1b", "\x1b[", "\x1b[<", "a", "Z", "0", "9", "\r", "\x7f", "\t",
		"\x1b\x1b", "\x1b\x1b[A",
		"\x1b[38;2;255;255;255m",
		"\x1b[0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0;0M",
		"\x1b[1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1;1M",
		"\x1b[<128;10;32M", "\x1b[<31;10;32M", "\x1b[<63;10;32M",
		"\x1b[<0;10;32M\x1b[<0;10;32m",
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
