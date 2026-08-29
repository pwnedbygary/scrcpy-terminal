package main

import "testing"

func TestCtrlMinus(t *testing.T) {
	a := &app{
		events:  make(chan inputEvent, 64),
		kb:      newKeyboard(),
		grabbed: true,
		audio:   &audioSink{gain: 256},
		tui:     &tui{cols: 80, rows: 39, keys: make([]uint64, 80*39), prev: make([]uint64, 80*39)},
		stream:  &streamState{videoW: 1280, videoH: 720},
		sess:    &session{deviceName: "test"},
		ctrl:    &controller{},
	}
	a.stream.updateGeometry()
	for _, s := range [][]byte{
		{0x1f},
		[]byte("\x1b[27;5;45~"),
		[]byte("\x1b[45;5u"),
		[]byte("\x1b-"),
		[]byte("\x1b[1;5-"),
	} {
		a.handleInput(s)
	}
	t.Log("no panic")
}
