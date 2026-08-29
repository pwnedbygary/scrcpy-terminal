package main

import "testing"

// User report: Ctrl+[ and Ctrl+= crash. Ctrl+[ = bare ESC (0x1B).
// Ctrl+= in Konsole sends `\x1b[27;5;61~` (CSI-u) or `\x1b[61;5u` (kitty).
func TestCtrlBracketEquals(t *testing.T) {
	a := &app{
		events:  make(chan inputEvent, 64),
		kb:      newKeyboard(),
		grabbed: true,
		audio:   &audioSink{gain: 256},
		cfg:     config{audio: true},
		tui:     &tui{cols: 80, rows: 39, keys: make([]uint64, 80*39), prev: make([]uint64, 80*39)},
		stream:  &streamState{videoW: 1280, videoH: 720},
		sess:    &session{deviceName: "test"},
		ctrl:    &controller{},
	}
	a.stream.updateGeometry()

	for _, s := range [][]byte{
		{0x1b},                  // Ctrl+[
		[]byte("\x1b[27;5;61~"), // Ctrl+= CSI-u (Konsole)
		[]byte("\x1b[61;5u"),    // Ctrl+= kitty
		[]byte("\x1b[27;1;61~"), // Ctrl+= no mod
		[]byte("\x1b[27;5;45~"), // Ctrl+-
		{0x1f},                  // Ctrl+- bare
		{0x1d},                  // Ctrl+]
		[]byte("\x1b[27;5;93~"), // Ctrl+]
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC on %q: %v", s, r)
				}
			}()
			a.handleInput(s)
		}()
	}
	t.Log("no panic on Ctrl+[/=/-/] variants")
}
