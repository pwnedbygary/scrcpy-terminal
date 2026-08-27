package main

import (
	"testing"
)

func TestFeedMouse(t *testing.T) {
	tui := &TUI{}
	cases := []struct {
		name string
		in   string
		want []event
	}{
		{"press left", "\x1b[<0;10;5M", []event{{kind: evMouse, btn: 0, x: 10, y: 5, press: true}}},
		{"motion drag", "\x1b[<32;12;7M", []event{{kind: evMouse, btn: 32, x: 12, y: 7, press: true, motion: true}}},
		{"release", "\x1b[<0;12;7m", []event{{kind: evMouse, btn: 0, x: 12, y: 7, press: false}}},
		{"wheel up", "\x1b[<68;10;5M", []event{{kind: evMouse, btn: 68, x: 10, y: 5, press: true, wheel: true}}},
		{"wheel down", "\x1b[<70;10;5M", []event{{kind: evMouse, btn: 70, x: 10, y: 5, press: true, wheel: true}}},
		{"left drag w/ ctrl", "\x1b[<34;20;8M", []event{{kind: evMouse, btn: 34, x: 20, y: 8, press: true, motion: true}}},
		{"f12", "\x1b[24~", []event{{kind: evFKey, code: 24}}},
		{"f1", "\x1b[11~", []event{{kind: evFKey, code: 11}}},
	}
	for _, c := range cases {
		var evs []event
		tui.feed([]byte(c.in), &evs)
		if len(evs) != len(c.want) {
			t.Fatalf("%s: got %d events, want %d (%+v)", c.name, len(evs), len(c.want), evs)
		}
		got := evs[0]
		w := c.want[0]
		if got.kind != w.kind || got.btn != w.btn || got.x != w.x || got.y != w.y ||
			got.press != w.press || got.motion != w.motion || got.wheel != w.wheel {
			t.Errorf("%s: got %+v want %+v", c.name, got, w)
		}
	}
}

func TestFeedMultiple(t *testing.T) {
	// a rapid burst of press + moves + release, as during a drag
	in := "\x1b[<0;40;20M\x1b[<32;42;20M\x1b[<32;45;22M\x1b[<0;45;22m"
	tui := &TUI{}
	var evs []event
	tui.feed([]byte(in), &evs)
	if len(evs) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(evs), evs)
	}
	if !evs[0].press || evs[0].btn != 0 {
		t.Errorf("first event should be press-left: %+v", evs[0])
	}
	if !evs[1].motion || evs[1].x != 42 {
		t.Errorf("second event should be motion at x=42: %+v", evs[1])
	}
	if evs[3].press || evs[3].btn != 0 {
		t.Errorf("last event should be release: %+v", evs[3])
	}
}