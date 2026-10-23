package main

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestWebGainOp: the gain op as any client can send it. An absent or negative
// gain leaves the volume alone, mute:true mutes and keeps the volume for the
// unmute, mute:false brings that volume back, and a gain sets the volume (the
// sink clamps it to 0..200). The messages are wire JSON, so an absent field
// really is absent.
func TestWebGainOp(t *testing.T) {
	for _, tc := range []struct {
		name string
		from int // volume before the messages, in percent
		msgs []string
		want int
	}{
		{"unmute without a gain leaves an unmuted sink alone", 100,
			[]string{`{"op":"gain","mute":false}`}, 100},
		{"a negative gain changes nothing", 70,
			[]string{`{"op":"gain","gain":-1}`}, 70},
		{"neither gain nor mute changes nothing", 70,
			[]string{`{"op":"gain"}`}, 70},
		{"mute mutes", 70,
			[]string{`{"op":"gain","mute":true}`}, 0},
		{"unmute brings back the volume from before the mute", 70,
			[]string{`{"op":"gain","mute":true}`, `{"op":"gain","mute":false}`}, 70},
		{"muting twice still brings back the volume from before", 70,
			[]string{`{"op":"gain","mute":true}`, `{"op":"gain","mute":true}`, `{"op":"gain","mute":false}`}, 70},
		{"unmute with a negative gain brings back the volume from before", 70,
			[]string{`{"op":"gain","mute":true}`, `{"op":"gain","gain":-1,"mute":false}`}, 70},
		{"a gain sets the volume", 100,
			[]string{`{"op":"gain","gain":40}`}, 40},
		{"a gain of 0 mutes, and unmute brings back the volume from before", 70,
			[]string{`{"op":"gain","gain":0}`, `{"op":"gain","mute":false}`}, 70},
		{"a gain above the range is clamped", 100,
			[]string{`{"op":"gain","gain":250}`}, 200},
		{"a gain sent with mute is the volume unmute brings back", 100,
			[]string{`{"op":"gain","gain":50,"mute":true}`, `{"op":"gain","mute":false}`}, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{audio: &audioSink{gain: 256, pct: 100}}
			s := &webServer{app: a}
			a.audio.setGain(tc.from)
			for _, msg := range tc.msgs {
				var ev webEvent
				if err := json.Unmarshal([]byte(msg), &ev); err != nil {
					t.Fatalf("%s: %v", msg, err)
				}
				s.applyControl(nil, &ev)
			}
			if got := a.audio.gainPercent(); got != tc.want {
				t.Errorf("volume %d%% after %v, want %d%%", got, tc.msgs, tc.want)
			}
		})
	}
}

// TestWebGainOpLeavesAStoppedSoundAlone: once the session's sound has stopped
// (here the device turns it off), no gain op changes this computer's volume,
// as the terminal's volume keys change nothing then either.
func TestWebGainOpLeavesAStoppedSoundAlone(t *testing.T) {
	stream := newStreamState(&session{deviceName: "Pixel 8", audio: soundOff(t)}, config{audio: true}, nil)
	if err := stream.runAudio(nil); !errors.Is(err, errAudioOff) {
		t.Fatalf("runAudio: %v, want the device to turn the sound off", err)
	}
	a := &app{audio: &audioSink{gain: 256, pct: 100}, stream: stream}
	a.audio.setGain(70)
	s := &webServer{app: a}
	for _, msg := range []string{`{"op":"gain","gain":40}`, `{"op":"gain","mute":true}`,
		`{"op":"gain","gain":0}`, `{"op":"gain","mute":false}`, `{"op":"gain","gain":150,"mute":false}`} {
		var ev webEvent
		if err := json.Unmarshal([]byte(msg), &ev); err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
		s.applyControl(nil, &ev)
		if got := a.audio.gainPercent(); got != 70 {
			t.Errorf("volume %d%% after %s, want it left at 70%%", got, msg)
		}
	}
}
