package main

import "testing"

// The device-volume read drives a number the user reads off the HUD, so the
// parse is pinned against the real command output (which prefixes every line
// with a log tag) and against the shapes that must NOT be reported.
func TestParseDeviceVolume(t *testing.T) {
	ok := []struct {
		name string
		out  string
		idx  int
		max  int
	}{
		{
			"real output, log tags included",
			"[V] will control stream=3 (STREAM_MUSIC)\n" +
				"[V] will get volume\n" +
				"[V] Connecting to AudioService\n" +
				"[V] volume is 1 in range [0..15]\n",
			1, 15,
		},
		{"muted", "[V] volume is 0 in range [0..15]\n", 0, 15},
		{"different scale", "[V] volume is 4 in range [0..7]\n", 4, 7},
		{"no log tag", "volume is 10 in range [0..10]\n", 10, 10},
		{"at maximum", "[V] volume is 15 in range [0..15]\n", 15, 15},
	}
	for _, c := range ok {
		idx, max, got := parseDeviceVolume([]byte(c.out))
		if !got {
			t.Errorf("%s: parse failed, want idx=%d max=%d", c.name, c.idx, c.max)
			continue
		}
		if idx != c.idx || max != c.max {
			t.Errorf("%s: got idx=%d max=%d, want idx=%d max=%d", c.name, idx, max, c.idx, c.max)
		}
	}

	bad := []struct {
		name string
		out  string
	}{
		{"empty", ""},
		{"unrelated output", "[V] Connecting to AudioService\n"},
		{"unparsable", "[V] volume is lots in range [0..15]\n"},
		{"degenerate range", "[V] volume is 0 in range [0..0]\n"},
		{"index above maximum", "[V] volume is 20 in range [0..15]\n"},
		{"negative index", "[V] volume is -1 in range [0..15]\n"},
	}
	for _, c := range bad {
		if idx, max, got := parseDeviceVolume([]byte(c.out)); got {
			t.Errorf("%s: want no reading, got idx=%d max=%d", c.name, idx, max)
		}
	}
}

// A server that has not read the device yet must say so rather than report 0%,
// which a viewer would read as "muted".
func TestDeviceVolumeUnknownVsMuted(t *testing.T) {
	var s webServer
	s.devVolIdx.Store(-1)
	if _, _, _, got := s.deviceVolume(); got {
		t.Fatal("devVolIdx=-1 must report no reading")
	}

	s.devVolMax.Store(15)
	if _, _, _, got := s.deviceVolume(); got {
		t.Fatal("no reading must stay unknown even once a scale is known")
	}

	s.devVolIdx.Store(0)
	pct, idx, max, got := s.deviceVolume()
	if !got || pct != 0 || idx != 0 || max != 15 {
		t.Fatalf("muted: got pct=%d idx=%d max=%d ok=%v, want 0/0/15/true", pct, idx, max, got)
	}

	s.devVolIdx.Store(1)
	if pct, _, _, _ = s.deviceVolume(); pct != 6 {
		t.Errorf("1 of 15: got %d%%, want 6%%", pct)
	}

	s.devVolIdx.Store(15)
	if pct, _, _, _ = s.deviceVolume(); pct != 100 {
		t.Errorf("15 of 15: got %d%%, want 100%%", pct)
	}
}

// A scale of zero would divide by zero; it must be rejected as unknown.
func TestDeviceVolumeZeroScaleIsUnknown(t *testing.T) {
	var s webServer
	s.devVolIdx.Store(3)
	s.devVolMax.Store(0)
	if _, _, _, got := s.deviceVolume(); got {
		t.Fatal("max=0 must report no reading")
	}
}
