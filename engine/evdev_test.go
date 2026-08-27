package engine

import (
	"encoding/binary"
	"testing"
)

// feed a synthetic EV_KEY BTN_SOUTH+pressed + sync into the same
// translation the reader uses, verifying the state we'd send.
func TestEvdevButtonTranslation(t *testing.T) {
	r := &GamepadReader{absMax: map[int]int32{}}
	ev := make([]byte, 16)
	binary.LittleEndian.PutUint16(ev[8:], evKey)
	binary.LittleEndian.PutUint16(ev[10:], btnSouth)
	binary.LittleEndian.PutUint32(ev[12:], 1)
	typ := binary.LittleEndian.Uint16(ev[8:])
	code := binary.LittleEndian.Uint16(ev[10:])
	val := int32(binary.LittleEndian.Uint32(ev[12:]))
	if typ == evKey && code == btnSouth {
		r.state.A = val != 0
	}
	if !r.state.A {
		t.Fatal("BTN_SOUTH not translated to A")
	}
	r.state.A = false
	// axis
	ev2 := make([]byte, 16)
	binary.LittleEndian.PutUint16(ev2[8:], evAbs)
	binary.LittleEndian.PutUint16(ev2[10:], absX)
	binary.LittleEndian.PutUint32(ev2[12:], 32767)
	axis(binary.LittleEndian.Uint16(ev2[10:]), int32(binary.LittleEndian.Uint32(ev2[12:])), r)
	if r.state.LX <= 0 {
		t.Fatalf("ABS_X 32767 -> LX=%v want >0", r.state.LX)
	}
}
