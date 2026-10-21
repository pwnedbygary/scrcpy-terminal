package main

import (
	"slices"
	"strings"
	"testing"
)

// writingSink is a sink whose writes reach the C code's write call and fail
// there: its C sink is a zero one, which no open made valid (an open one fails
// there in pa_simple_write, when the sound server goes away). That C sink is
// Go memory, so it must never be closed: the test takes it back at its end.
func writingSink(t *testing.T) *audioSink {
	a := &audioSink{gain: 256, pct: 100}
	zeroed(&a.sink)
	t.Cleanup(func() {
		a.mu.Lock()
		a.sink = nil
		a.mu.Unlock()
	})
	return a
}

// zeroed points *p at a new zero T: a C struct, say, which a test cannot name
// (cgo does not work in test files).
func zeroed[T any](p **T) { *p = new(T) }

// TestSinkThatFailsWhilePlaying: a write that fails stops this computer's
// sound, from the goroutine that plays it, while the run loop, the status row
// and the browser read the sink's state from others. No read races the write
// (run with -race), and once it has failed, each of them sees it: the sink no
// longer plays and says why, the statistics line and the status row say audio
// off, the volume keys leave it alone, no follower starts for it, and it takes
// no more sound.
func TestSinkThatFailsWhilePlaying(t *testing.T) {
	const stopped = "scterm: audio sink write failed; audio stopped. Check `pactl list sinks` and your audio server.\n"
	forgetLogOnce(t, stopped)
	bin := t.TempDir()
	fakeProgram(t, bin, "pactl", "exit 1\n") // found on PATH, never run
	t.Setenv("PATH", bin)
	t.Setenv("SCT_AUDIO_DEVICE", "")
	a := statusApp(80, "Pixel 8")
	a.audio = writingSink(t)

	stderr := captureStderr(t, func() {
		wrote := make(chan struct{})
		go func() {
			defer close(wrote)
			a.audio.writePCM16(make([]byte, 16))
		}()
		for {
			a.audio.plays()
			a.audio.errString()
			audioState(a)
			a.statusLine()
			select {
			case <-wrote:
				return
			default:
			}
		}
	})

	if stderr != stopped {
		t.Errorf("stderr %q, want %q", stderr, stopped)
	}
	if a.audio.plays() {
		t.Error("the sink still plays")
	}
	if got, want := a.audio.errString(), "pulseaudio write failed (check: pipewire or pulseaudio running? `pactl info`)"; got != want {
		t.Errorf("why: %q, want %q", got, want)
	}
	if got := audioState(a); got != "off" {
		t.Errorf("statistics line: audio=%s, want audio=off", got)
	}
	if got := a.statusLine(); !strings.Contains(got, "audio off") || strings.Contains(got, "vol ") {
		t.Errorf("status row %q, want it to say audio off", got)
	}
	a.toggleLocalMute()
	a.adjustLocalVolume(-10)
	if got := a.audio.gainPercent(); got != 100 {
		t.Errorf("the volume keys set the volume of a sink that stopped to %d%%", got)
	}
	if _, ok := a.audioFollower(); ok {
		t.Error("a follower would start for a sink that stopped")
	}
	a.audio.setGain(50)
	pcm := []byte{0x00, 0x10, 0x00, 0xf0, 0x00, 0x20, 0x00, 0xe0}
	sent := slices.Clone(pcm)
	a.audio.writePCM16(pcm)
	if !slices.Equal(pcm, sent) {
		t.Errorf("a sink that stopped took more sound: %x became %x", sent, pcm)
	}
}
