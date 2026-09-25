package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The golden scrcpy wire bytes in protocol/fixtures are shared with the
// Android client (android/protocol), so both serializers are held to one
// hand-derived definition instead of to each other.

type wireFixtures struct {
	Control []struct {
		Name   string         `json:"name"`
		Type   int            `json:"type"`
		Fields map[string]any `json:"fields"`
		Hex    string         `json:"hex"`
	} `json:"control"`
	Device []struct {
		Name string `json:"name"`
		Hex  string `json:"hex"`
	} `json:"device"`
	FixedPoint []struct {
		Kind  string      `json:"kind"`
		Value json.Number `json:"value"`
		Raw   json.Number `json:"raw"`
	} `json:"fixedPoint"`
	Media []struct {
		Name   string         `json:"name"`
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
		Hex    string         `json:"hex"`
	} `json:"media"`
}

func loadWireFixtures(t *testing.T) wireFixtures {
	t.Helper()
	f, err := os.Open("protocol/fixtures/scrcpy_wire.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber() // sequence numbers exceed float64 precision
	var w wireFixtures
	if err := dec.Decode(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

func fixInt(t *testing.T, fields map[string]any, key string) int64 {
	t.Helper()
	n, ok := fields[key].(json.Number)
	if !ok {
		t.Fatalf("fixture field %q is not a number", key)
	}
	v, err := n.Int64()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fixPos(t *testing.T, f map[string]any) position {
	return position{
		x:       int32(fixInt(t, f, "x")),
		y:       int32(fixInt(t, f, "y")),
		screenW: uint16(fixInt(t, f, "screenWidth")),
		screenH: uint16(fixInt(t, f, "screenHeight")),
	}
}

// goldenControlWire builds a fixture message with this client's own helpers,
// returning ok=false for message types scterm never sends.
func goldenControlWire(t *testing.T, typ int, f map[string]any) ([]byte, bool) {
	switch typ {
	case ctrlInjectKeycode:
		return serializeControlMsg(keycodeMsg(uint32(fixInt(t, f, "action")), uint32(fixInt(t, f, "keycode")),
			uint32(fixInt(t, f, "repeat")), uint32(fixInt(t, f, "metaState"))), ctrlInjectKeycode), true
	case ctrlInjectText:
		text := f["text"].(string)
		payload := make([]byte, 1+4+len(text))
		copy(payload[5:], text)
		return serializeControlMsg(payload, ctrlInjectText), true
	case ctrlInjectTouch:
		return serializeControlMsg(touchMsg(byte(fixInt(t, f, "action")), uint64(fixInt(t, f, "pointerId")), fixPos(t, f),
			uint16(fixInt(t, f, "pressure")), uint32(fixInt(t, f, "actionButton")), uint32(fixInt(t, f, "buttons"))), ctrlInjectTouch), true
	case ctrlInjectScroll:
		// scterm scrolls vertically in whole notches; the fixtures encode
		// +/-1 notch as +/-2048.
		notches := float32(fixInt(t, f, "vScroll")) / 2048
		return serializeControlMsg(scrollMsg(fixPos(t, f), 0, notches, uint32(fixInt(t, f, "buttons"))), ctrlInjectScroll), true
	case ctrlBackOrScreenOn:
		return serializeControlMsg([]byte{byte(fixInt(t, f, "action"))}, ctrlBackOrScreenOn), true
	case ctrlExpandNotif, ctrlExpandSettings, ctrlCollapsePanels, ctrlRotateDevice, ctrlResetVideo:
		return serializeControlMsg(nil, byte(typ)), true
	case ctrlSetClipboard:
		text := f["text"].(string)
		msg := make([]byte, 11+len(text))
		binary.BigEndian.PutUint64(msg[0:8], uint64(fixInt(t, f, "sequence")))
		if f["paste"].(bool) {
			msg[8] = 1
		}
		copy(msg[11:], text)
		return serializeControlMsg(msg, ctrlSetClipboard), true
	case ctrlSetDisplayPower:
		on := byte(0)
		if f["on"].(bool) {
			on = 1
		}
		return serializeControlMsg([]byte{on}, ctrlSetDisplayPower), true
	case ctrlResizeDisplay:
		msg := make([]byte, 4)
		binary.BigEndian.PutUint16(msg[0:2], uint16(fixInt(t, f, "width")))
		binary.BigEndian.PutUint16(msg[2:4], uint16(fixInt(t, f, "height")))
		return serializeControlMsg(msg, ctrlResizeDisplay), true
	}
	return nil, false
}

func TestControlMessagesMatchSharedFixtures(t *testing.T) {
	w := loadWireFixtures(t)
	checked := 0
	for _, c := range w.Control {
		got, ok := goldenControlWire(t, c.Type, c.Fields)
		if !ok {
			continue
		}
		want, _ := hex.DecodeString(c.Hex)
		if !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %x\nwant %x", c.Name, got, want)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("only %d fixtures exercised", checked)
	}
}

func TestFixedPointMatchesSharedFixtures(t *testing.T) {
	for _, fp := range loadWireFixtures(t).FixedPoint {
		v, _ := fp.Value.Float64()
		raw, _ := fp.Raw.Int64()
		switch fp.Kind {
		case "u16":
			if got := floatToU16fp(float32(v)); int64(got) != raw {
				t.Errorf("u16(%v) = %d, want %d", v, got, raw)
			}
		case "scroll":
			if got := scrollToI16fp(float32(v)); int64(got) != raw {
				t.Errorf("scroll(%v) = %d, want %d", v, got, raw)
			}
		}
	}
}

func TestDeviceMessagesConsumeExactlyOneMessage(t *testing.T) {
	for _, d := range loadWireFixtures(t).Device {
		b, _ := hex.DecodeString(d.Hex)
		n, ok := parseDeviceMessage(b)
		if !ok || n != len(b) {
			t.Errorf("%s: consumed %d/%d (complete=%v)", d.Name, n, len(b), ok)
		}
		if n, ok := parseDeviceMessage(b[:len(b)-1]); ok {
			t.Errorf("%s: truncated message reported complete (%d bytes)", d.Name, n)
		}
	}
}

func TestMediaHeadersMatchSharedFixtures(t *testing.T) {
	for _, m := range loadWireFixtures(t).Media {
		b, _ := hex.DecodeString(m.Hex)
		switch m.Kind {
		case "codec":
			id, err := readCodecID(bytes.NewReader(b))
			want := map[string]int{"h264": codecH264, "opus": codecOpus}[m.Fields["codec"].(string)]
			if err != nil || id != want {
				t.Errorf("%s: codec %#x, want %#x (%v)", m.Name, id, want, err)
			}
		case "session":
			isSession, _, _, _, _ := parsePacketHeader(b)
			w, h, resize := sessionHeader(b)
			if !isSession || int64(w) != fixInt(t, m.Fields, "width") || int64(h) != fixInt(t, m.Fields, "height") ||
				resize != m.Fields["clientResize"].(bool) {
				t.Errorf("%s: session=%v %dx%d resize=%v", m.Name, isSession, w, h, resize)
			}
		case "packet":
			isSession, pts, isConfig, isKey, size := parsePacketHeader(b[:12])
			if isSession || pts != fixInt(t, m.Fields, "ptsUs") || isConfig != m.Fields["config"].(bool) ||
				isKey != m.Fields["keyFrame"].(bool) || int(size) != len(b)-12 {
				t.Errorf("%s: session=%v pts=%d config=%v key=%v size=%d", m.Name, isSession, pts, isConfig, isKey, size)
			}
		}
	}
}
