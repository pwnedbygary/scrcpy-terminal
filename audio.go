package main

/*
#cgo CFLAGS: -O3 -mavx2 -fomit-frame-pointer -I${SRCDIR}
#cgo pkg-config: libpulse-simple
#include <pulse/simple.h>
#include <pulse/error.h>
#include <stdlib.h>
#include <string.h>

// audioSink wraps pa_simple.
typedef struct {
    pa_simple *s;
    int err;
    int valid;
} sct_audio_sink;

static sct_audio_sink *sct_audio_open_with_server(const char *app_name, const char *server, const char *device) {
    sct_audio_sink *a = calloc(1, sizeof(*a));
    if (!a) return NULL;
    pa_sample_spec ss;
    ss.format = PA_SAMPLE_S16LE;
    ss.rate = 48000;
    ss.channels = 2;
    pa_buffer_attr attr;
    attr.maxlength = (uint32_t) -1;
    attr.tlength = 48000 * 2 * 2 / 20; // 50ms
    attr.prebuf = (uint32_t) -1;
    attr.minreq = (uint32_t) -1;
    attr.fragsize = (uint32_t) -1;
    a->s = pa_simple_new(server, app_name, PA_STREAM_PLAYBACK, device,
                         "sct audio", &ss, NULL, &attr, &a->err);
    a->valid = a->s != NULL;
    return a;
}

static sct_audio_sink *sct_audio_open(const char *app_name) {
    return sct_audio_open_with_server(app_name, NULL, NULL);
}

static int sct_audio_write(sct_audio_sink *a, const uint8_t *data, int len) {
    if (!a || !a->valid) return -1;
    if (pa_simple_write(a->s, data, (size_t) len, &a->err) < 0) return -1;
    return 0;
}

// Returns the last pa error string (caller frees). NULL if none.
static char *sct_audio_last_error(sct_audio_sink *a) {
    if (!a || !a->err) return NULL;
    char *s = strdup(pa_strerror(a->err));
    return s;
}

static void sct_audio_close(sct_audio_sink *a) {
    if (!a) return;
    if (a->s) {
        pa_simple_drain(a->s, &a->err);
        pa_simple_free(a->s);
    }
    free(a);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// audioSink plays s16le stereo 48kHz with a software gain (mute = gain 0).
type audioSink struct {
	mu   sync.Mutex
	sink *C.sct_audio_sink
	gain int32 // q8, 256 = 1.0
	err  error
}

func newAudioSink() *audioSink {
	a := &audioSink{gain: 256}
	a.open()
	return a
}

// open (re)connects to PulseAudio. Retries a few times with a backoff since
// audio servers (PipeWire) may be starting up. Reports the real PA error.
func (a *audioSink) open() {
	var lastErr string
	// First try the default server (respects PULSE_SERVER / the session
	// socket). If that fails, fall back to the explicit runtime-dir socket
	// so terminals with a stale PULSE_SERVER still get sound.
	servers := []string{"", pulseSocketPath()}
	devices := audioDevices()
	for _, server := range servers {
		for _, device := range devices {
			for attempt := 0; attempt < 3; attempt++ {
				sink := C.sct_audio_open_with_server(cstr("sct"), cstr(server), cstr(device))
				if sink != nil && sink.valid != 0 {
					fmt.Fprintf(stderrWriter(), "sct: audio sink: %s (device %q)\n",
						serverOrDefault(server), deviceOrDefault(device))
					a.sink = sink
					a.err = nil
					return
				}
				if sink != nil {
					if es := C.sct_audio_last_error(sink); es != nil {
						lastErr = C.GoString(es)
						C.free(unsafe.Pointer(es))
					}
					C.sct_audio_close(sink)
				}
				time.Sleep(time.Duration(150*(attempt+1)) * time.Millisecond)
			}
			lastErr = fmt.Sprintf("server %q device %q: %s", serverOrDefault(server), deviceOrDefault(device), lastErr)
		}
	}
	if lastErr == "" {
		lastErr = "pulseaudio not available"
	}
	a.err = fmt.Errorf("audio sink: %s", lastErr)
}

// audioDevices returns the sink device candidates, in priority order:
//  1. SCT_AUDIO_DEVICE env override (explicit user choice)
//  2. the sink currently receiving audio (active sink-inputs) - this is
//     "the actively selected device" on the user's desktop
//  3. physical ALSA sinks that are RUNNING (in use by the session)
//  4. any physical ALSA sink
//  5. the server default (empty string)
//
// The default sink may be a virtual one (e.g. a game-streaming sink)
// that silently buffers audio nobody hears, hence it is last.
func audioDevices() []string {
	if d := envOr("SCT_AUDIO_DEVICE", ""); d != "" {
		return []string{d}
	}

	// Which sinks have streams attached right now?
	type sinkState struct {
		id      string
		name    string
		running bool
		inputs  int
	}
	var sinks []sinkState
	if out, err := pulseCmd("list", "sinks", "short"); err == nil {
		for _, line := range splitLines(string(out)) {
			fields := splitFields(line)
			if len(fields) >= 5 {
				sinks = append(sinks, sinkState{
					id:      fields[0],
					name:    fields[1],
					running: fields[4] == "RUNNING",
				})
			}
		}
	}

	byID := map[string]int{}
	for i, s := range sinks {
		byID[s.id] = i
	}
	if out, err := pulseCmd("list", "sink-inputs", "short"); err == nil {
		for _, line := range splitLines(string(out)) {
			fields := splitFields(line)
			if len(fields) >= 2 {
				if i, ok := byID[fields[1]]; ok {
					sinks[i].inputs++
				}
			}
		}
	}

	// 2) active sinks (have streams) - prefer the one with most inputs
	var active []string
	for _, s := range sinks {
		if s.inputs > 0 {
			active = append(active, s.name)
		}
	}
	if len(active) > 0 {
		return active
	}

	// 3) RUNNING physical ALSA sinks
	var running []string
	// 4) all physical ALSA sinks
	var physical []string
	for _, s := range sinks {
		if !hasPrefix(s.name, "alsa_output.") {
			continue
		}
		physical = append(physical, s.name)
		if s.running {
			running = append(running, s.name)
		}
	}
	if len(running) > 0 {
		return running
	}
	if len(physical) > 0 {
		return physical
	}
	// 5) server default
	return []string{""}
}

func serverOrDefault(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

func deviceOrDefault(d string) string {
	if d == "" {
		return "default"
	}
	return d
}

// pulseSocketPath returns the standard user pulse socket, or "".
func pulseSocketPath() string {
	if dir := envOr("XDG_RUNTIME_DIR", ""); dir != "" {
		return "unix:" + dir + "/pulse/native"
	}
	return ""
}

func (a *audioSink) errString() string {
	if a.err == nil {
		return ""
	}
	return fmt.Sprintf("%v (check: pipewire or pulseaudio running? `pactl info`)", a.err)
}

func errNoAudio() error {
	return errPulseUnavailable{}
}

type errPulseUnavailable struct{}

func (errPulseUnavailable) Error() string {
	return "pulseaudio not available"
}

func cstr(s string) *C.char {
	return C.CString(s)
}

// writePCM16 feeds interleaved s16 stereo samples to the sink (in place gain).
func (a *audioSink) writePCM16(pcm []byte) {
	if a.err != nil {
		return
	}
	if len(pcm)%4 != 0 {
		pcm = pcm[:len(pcm)-len(pcm)%4]
	}
	g := int32(atomic.LoadInt32(&a.gain))
	if g == 0 {
		return // mute: drop quickly, pa_simple will drain
	}
	if g != 256 {
		samples := int16Slice(pcm)
		gainS16(samples, g)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sink == nil {
		return
	}
	if C.sct_audio_write(a.sink, (*C.uint8_t)(bytePtr(pcm)), C.int(len(pcm))) != 0 {
		a.err = errPulseLagged{}
		logOnce("sct: audio sink write failed; audio stopped. Check `pactl list sinks` and your audio server.\n")
	}
}

type errPulseLagged struct{}

func (errPulseLagged) Error() string { return "pulseaudio write failed" }

// setGain sets volume in percent (0..200). Mute = 0.
func (a *audioSink) setGain(pct int) {
	if pct < 0 {
		pct = 0
	}
	if pct > 200 {
		pct = 200
	}
	atomic.StoreInt32(&a.gain, int32(pct*256/100))
}

func (a *audioSink) gainPercent() int {
	return int(atomic.LoadInt32(&a.gain)) * 100 / 256
}

func (a *audioSink) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sink != nil {
		C.sct_audio_close(a.sink)
		a.sink = nil
	}
}
