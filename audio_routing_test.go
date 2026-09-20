package main

// Audio routing: which output plays the device sound.
//
// In web/window mode the browser plays the audio (fed by the PCM tap), so by
// default the host sink is muted and the stream is the sole source of sound.
// --audio-dup is what asks for the sound in both places. Before this, the host
// sink and the browser both played unconditionally, so every sound was doubled
// with a small offset between the two outputs.
//
// The decision is a pure function of the config so it can be pinned here: the
// sink itself needs a real PulseAudio device to observe.

import "testing"

func TestHostAudioSilent(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		want bool
	}{
		{"web: stream is the sole source", config{web: true}, true},
		{"window (sets web): stream is the sole source", config{web: true, window: true}, true},
		{"web + audio-dup: both outputs play", config{web: true, audioDup: true}, false},
		{"tui: host sink is the only output", config{}, false},
		{"tui + audio-dup: host still plays", config{audioDup: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostAudioSilent(tc.cfg); got != tc.want {
				t.Errorf("hostAudioSilent(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}
