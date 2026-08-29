package main

import "testing"

// audio disabled (no pulse sink): volume keys must be no-ops, never panic.
func TestVolumeWithDeadAudio(t *testing.T) {
	a := &app{
		events:      make(chan inputEvent, 64),
		paletteKeys: []int{3},
		grabbed:     true,
		audio:       &audioSink{gain: 100 * 256 / 100, err: errPulseUnavailable{}},
		cfg:         config{audio: true},
		tui:         &tui{cols: 80, rows: 39, keys: make([]uint64, 80*39), prev: make([]uint64, 80*39)},
		stream:      &streamState{videoW: 1280, videoH: 720},
		sess:        &session{deviceName: "test"},
		ctrl:        &controller{},
	}
	a.stream.updateGeometry()
	for _, s := range [][]byte{{0x1f}, []byte("\x1b[27;5;45~"), []byte("\x1b-"), []byte("\x1b[1;5-"),
		[]byte("\x1bm"), []byte("\x1b[<0;10;32M"), []byte("\x1b[24~")} {
		before := a.audio.gainPercent()
		a.handleInput(s)
		if a.audio.gainPercent() != before {
			t.Fatalf("seq %q changed gain on dead sink: %d -> %d", s, before, a.audio.gainPercent())
		}
	}
	t.Log("all volume/mute/mouse keys no-op on dead sink")
}
