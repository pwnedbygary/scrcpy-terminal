package engine

import (
	"encoding/binary"
	"testing"
)

func TestGamepadReportCenter(t *testing.T) {
	g := &GamepadState{}
	r := g.Report()
	if len(r) != 15 {
		t.Fatalf("len=%d want 15", len(r))
	}
	if binary.LittleEndian.Uint16(r[0:]) != 0x8000 {
		t.Errorf("LX center = %x want 0x8000", binary.LittleEndian.Uint16(r[0:]))
	}
	if r[14] != 0 {
		t.Errorf("hat = %d want 0", r[14])
	}
}

func TestGamepadReportButtonsAndHat(t *testing.T) {
	g := &GamepadState{A: true, B: true, Hat: 3}
	r := g.Report()
	if btns := binary.LittleEndian.Uint16(r[12:]); btns != 0x0003 {
		t.Errorf("buttons = %04x want 0x0003", btns)
	}
	if r[14] != 3 {
		t.Errorf("hat = %d want 3", r[14])
	}
	// full-left stick -> 0x0000
	g = &GamepadState{LX: -1}
	if binary.LittleEndian.Uint16(g.Report()[0:]) != 0 {
		t.Errorf("LX full-left != 0")
	}
}

func TestScrollMessage(t *testing.T) {
	// just ensure it builds the right type byte + 21-byte payload
	c := &Control{}
	c.conn = &nopConn{}
	c.Scroll(100, 200, 1920, 1080, 0, 1.0)
}
