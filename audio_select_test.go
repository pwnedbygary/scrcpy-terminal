package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakePactl isolates libpulse and pactl (isolatePulse), then puts a pactl on
// PATH, the only program there, that answers audioDevices' three questions
// with the given listings.
func fakePactl(t *testing.T, defaultSink string, sinks, inputs []string) {
	t.Helper()
	bin := filepath.Join(isolatePulse(t), "bin")
	t.Setenv("FAKE_PACTL_DEFAULT", defaultSink)
	t.Setenv("FAKE_PACTL_SINKS", strings.Join(sinks, "\n"))
	t.Setenv("FAKE_PACTL_INPUTS", strings.Join(inputs, "\n"))
	const script = `#!/bin/sh
case "$*" in
"get-default-sink") printf '%s\n' "$FAKE_PACTL_DEFAULT" ;;
"list sinks short") printf '%s\n' "$FAKE_PACTL_SINKS" ;;
"list sink-inputs short") printf '%s\n' "$FAKE_PACTL_INPUTS" ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "pactl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestAudioDevicesFollowTheirOrder: the sink named in SCT_AUDIO_DEVICE; else
// the default sink when it is a sound card; else the sinks other programs
// play on; else the sound cards pactl reports running; else every sound
// card; else the server's default (""), which is all there is without pactl.
// The listings are in the format of `pactl list sinks short` and `pactl list
// sink-inputs short`: columns separated by tabs, and spaces inside the
// sample spec.
func TestAudioDevicesFollowTheirOrder(t *testing.T) {
	const (
		analog    = "alsa_output.pci-0000_00_1f.3.analog-stereo"
		hdmi      = "alsa_output.pci-0000_01_00.1.hdmi-stereo"
		bluetooth = "bluez_output.00_1B_66_AA_BB_CC.1"
		virtual   = "sink-sunshine-stereo"
	)
	sink := func(id, name, state string) string {
		return id + "\t" + name + "\tPipeWire\ts32le 2ch 48000Hz\t" + state
	}
	input := func(id, sinkID string) string {
		return id + "\t" + sinkID + "\t63\tPipeWire\tfloat32le 2ch 48000Hz"
	}
	for _, tc := range []struct {
		name, env, def string
		sinks, inputs  []string
		want           []string
	}{
		{
			name: "the sink SCT_AUDIO_DEVICE names", env: "my_sink", def: analog,
			sinks: []string{sink("47", analog, "SUSPENDED")},
			want:  []string{"my_sink"},
		},
		{
			name: "the default sink, a sound card", def: analog,
			sinks: []string{sink("47", analog, "SUSPENDED"), sink("48", hdmi, "RUNNING")},
			want:  []string{analog},
		},
		{
			name: "the sinks other programs play on", def: virtual,
			sinks:  []string{sink("47", analog, "RUNNING"), sink("48", bluetooth, "RUNNING"), sink("49", virtual, "IDLE")},
			inputs: []string{input("71", "48")},
			want:   []string{bluetooth},
		},
		{
			name: "a running sound card", def: virtual,
			sinks: []string{sink("47", analog, "SUSPENDED"), sink("48", hdmi, "RUNNING"), sink("50", virtual, "SUSPENDED")},
			want:  []string{hdmi},
		},
		{
			name: "every sound card, in pactl's order", def: virtual,
			sinks: []string{sink("47", analog, "IDLE"), sink("48", hdmi, "SUSPENDED"), sink("50", virtual, "SUSPENDED")},
			want:  []string{analog, hdmi},
		},
		{
			name: "the server's default, with no sound card listed", def: bluetooth,
			sinks: []string{sink("48", bluetooth, "IDLE"), sink("50", virtual, "SUSPENDED")},
			want:  []string{""},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakePactl(t, tc.def, tc.sinks, tc.inputs)
			if tc.env != "" {
				t.Setenv("SCT_AUDIO_DEVICE", tc.env)
			}
			if got := audioDevices(); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("the server's default, without pactl", func(t *testing.T) {
		isolatePulse(t)
		if got, want := audioDevices(), []string{""}; !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
