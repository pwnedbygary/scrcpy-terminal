package main

/*
#cgo CFLAGS: -O3 -fomit-frame-pointer -I${SRCDIR}
#cgo amd64 CFLAGS: -mavx2
#cgo pkg-config: libpulse-simple
#include <pulse/simple.h>
#include <pulse/error.h>
#include <unistd.h>
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
    // Low-latency buffer: reduce tlength from the previous 50ms to 20ms to cut
    // host-side latency, but leave prebuf/minreq/fragsize at PA defaults (-1):
    // forcing all of them to a small fixed size on a playback stream makes
    // PulseAudio cork the stream (it can't honor a sub-sink-minimum buffer,
    // e.g. HDMI), which manifests as total silence instead of underrun.
    pa_buffer_attr attr;
    attr.maxlength = (uint32_t) -1;
    attr.tlength = (uint32_t) 48000 * 2 * 2 / 50; // 20ms
    attr.prebuf = (uint32_t) -1;
    attr.minreq = (uint32_t) -1;
    attr.fragsize = (uint32_t) -1;
    a->s = pa_simple_new(server, app_name, PA_STREAM_PLAYBACK, device,
                         "scterm audio", &ss, NULL, &attr, &a->err);
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

// Kill all sink-inputs belonging to a given application name (kills leftover
// streams from dead or stray sct processes). Returns #killed.
#include <pulse/introspect.h>
#include <pulse/mainloop.h>

typedef struct {
    const char *app;
    int killed;
} sct_killer;

static void sct_killer_cb(pa_context *c, const pa_sink_input_info *i, int eol, void *ud) {
    sct_killer *k = ud;
    if (eol > 0) return; // end of list: the operation finishes naturally
    if (!eol && i && i->proplist) {
        const char *name = pa_proplist_gets(i->proplist, PA_PROP_APPLICATION_NAME);
        if (name && strcmp(name, k->app) == 0) {
            // Runs before our own stream opens, so any "sct" stream found
            // here belongs to a previous/dead/stray instance: silence it.
            pa_context_kill_sink_input(c, i->index, NULL, NULL);
            k->killed++;
        }
    }
}

// Kills all sink-inputs whose application.name == app. Returns count killed,
// -1 on connection failure. Runs a private mainloop; no shared state.
static int sct_audio_kill_streams(const char *server, const char *app) {
    pa_mainloop *ml = pa_mainloop_new();
    if (!ml) return -1;
    pa_mainloop_api *api = pa_mainloop_get_api(ml);
    pa_context *ctx = pa_context_new(api, "scterm-cleanup");
    if (!ctx) { pa_mainloop_free(ml); return -1; }
    if (server == NULL || server[0] == '\0') server = NULL;
    if (pa_context_connect(ctx, server, PA_CONTEXT_NOFLAGS, NULL) < 0) {
        pa_context_unref(ctx); pa_mainloop_free(ml); return -1;
    }
    sct_killer kk = { app, 0 };
    // Wait until READY (bounded)
    for (int i = 0; i < 300; i++) {
        pa_context_state_t st = pa_context_get_state(ctx);
        if (st == PA_CONTEXT_READY) break;
        if (st == PA_CONTEXT_FAILED || st == PA_CONTEXT_TERMINATED) {
            pa_context_disconnect(ctx); pa_context_unref(ctx); pa_mainloop_free(ml);
            return -1;
        }
        pa_mainloop_iterate(ml, 1, NULL);
    }
    if (pa_context_get_state(ctx) != PA_CONTEXT_READY) {
        pa_context_disconnect(ctx); pa_context_unref(ctx); pa_mainloop_free(ml);
        return -1;
    }
    pa_operation *op = pa_context_get_sink_input_info_list(ctx, sct_killer_cb, &kk);
    if (op) {
        while (pa_operation_get_state(op) == PA_OPERATION_RUNNING) {
            pa_mainloop_iterate(ml, 1, NULL);
        }
        pa_operation_unref(op);
    }
    // The kill-sink-input requests are queued in libpulse's send buffer;
    // tearing the mainloop down now would discard them and the stranded
    // stream would keep playing. Pump non-blocking for a moment so the
    // kills actually reach the server.
    for (int i = 0; i < 30; i++) {
        pa_mainloop_iterate(ml, 0, NULL);
        usleep(10000); // 300ms total
    }
    pa_context_disconnect(ctx);
    pa_context_unref(ctx);
    pa_mainloop_free(ml);
    return kk.killed;
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
        // NOTE: no pa_simple_drain here. If the sink is suspended (HDMI TV
        // off, etc.) drain blocks forever, the process never exits and the
        // stream keeps playing. pa_simple_free drops the buffer and closes
        // the connection immediately - that is what "stop the sound now"
        // must mean when the app quits.
        pa_simple_free(a->s);
    }
    free(a);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// audioSink plays s16le stereo 48kHz with a software gain (mute = gain 0).
type audioSink struct {
	mu   sync.Mutex
	sink *C.sct_audio_sink
	gain int32 // q8, 256 = 1.0, derived from pct
	// err is why the sink cannot play at all, if it cannot: open's error, or
	// errNoSound. It is set before the sink is shared and never changes after;
	// a write that fails later, on the playback goroutine, sets writeFailed
	// instead. Read both through failure.
	err         error
	writeFailed atomic.Bool

	// pct is the volume in whole percent, and unmutePct the volume the last
	// mute replaced. volMu keeps them and gain changing together.
	volMu     sync.Mutex
	pct       int32
	unmutePct int32

	// silent drops PCM instead of writing it to the host device. Web/window
	// mode sets it: there the browser is the audio output, and playing on the
	// host as well would double every sound with a small offset, which for a
	// window on the same machine is plainly wrong. This is independent of
	// fanPCM, so browser audio is unaffected.
	silent atomic.Bool
}

func newAudioSink() *audioSink {
	a := &audioSink{gain: 256, pct: 100}
	a.open()
	return a
}

// errNoSound is the error of the sink of a session without sound, which is
// never opened (noAudioSink).
var errNoSound = errors.New("the session has no sound")

// noAudioSink is the sink of a session without sound. It never connects to
// the sound server, so the session does not show in the desktop's mixer and
// does not wait for a server that is not there. Its error makes every use
// treat it as a sink that cannot play.
func noAudioSink() *audioSink {
	return &audioSink{gain: 256, pct: 100, err: errNoSound}
}

// plays reports whether the sink plays the sound on this computer: it
// opened, has not failed since, and is not kept silent for the browser.
func (a *audioSink) plays() bool {
	return a != nil && a.failure() == nil && !a.silent.Load()
}

// failure is why the sink does not play: why it never opened, or the write
// that failed since; nil while it works. The run loop, the status row and the
// browser ask from other goroutines than the one that writes.
func (a *audioSink) failure() error {
	switch {
	case a.err != nil:
		return a.err
	case a.writeFailed.Load():
		return errPulseLagged{}
	}
	return nil
}

// open connects to PulseAudio and opens the stream on the first server and
// sink that work (see openFirst). Reports the real PA errors.
func (a *audioSink) open() {
	name := C.CString("scterm")
	defer C.free(unsafe.Pointer(name))
	server, device, err := openFirst(audioServers(), audioDevices(), func(server, device string) (int, string) {
		cserver, cdevice := cstrOrNil(server), cstrOrNil(device)
		defer C.free(unsafe.Pointer(cserver))
		defer C.free(unsafe.Pointer(cdevice))
		sink := C.sct_audio_open_with_server(name, cserver, cdevice)
		if sink == nil {
			return int(C.PA_ERR_INTERNAL), "out of memory"
		}
		if sink.valid != 0 {
			a.sink = sink
			return 0, ""
		}
		var msg string
		if es := C.sct_audio_last_error(sink); es != nil {
			msg = C.GoString(es)
			C.free(unsafe.Pointer(es))
		}
		code := int(sink.err)
		C.sct_audio_close(sink)
		return code, msg
	}, audioPause)
	a.err = err
	if err == nil {
		noticef("scterm: audio sink: %s (device %q)\n",
			serverOrDefault(server), deviceOrDefault(device))
	}
}

// audioTries is how many times openFirst tries a server and sink.
const audioTries = 3

// audioPause waits between open's rounds of tries.
var audioPause = time.Sleep

// openFirst tries each server with each device, in that order, and returns
// the first pair that opens. try makes one attempt and returns 0, or the
// libpulse error code and text. Pairs that fail are tried again, in later
// rounds 150 and 300 ms apart, since audio servers (PipeWire) may be starting
// up; a pair whose arguments were refused is not (see paRetry). The error
// gives the last failure of every pair.
func openFirst(servers, devices []string, try func(server, device string) (int, string), sleep func(time.Duration)) (string, string, error) {
	type pair struct {
		server, device, err string
		refused             bool
	}
	var pairs []pair
	for _, server := range servers {
		for _, device := range devices {
			pairs = append(pairs, pair{server: server, device: device})
		}
	}
	for round := 0; round < audioTries; round++ {
		if round > 0 {
			sleep(time.Duration(150*round) * time.Millisecond)
		}
		again := false
		for i := range pairs {
			p := &pairs[i]
			if p.refused {
				continue
			}
			code, msg := try(p.server, p.device)
			if code == 0 {
				return p.server, p.device, nil
			}
			p.err, p.refused = msg, !paRetry(code)
			again = again || !p.refused
		}
		if !again {
			break
		}
	}
	var errs []string
	for _, p := range pairs {
		errs = append(errs, fmt.Sprintf("server %q device %q: %s",
			serverOrDefault(p.server), deviceOrDefault(p.device), p.err))
	}
	if len(errs) == 0 {
		errs = append(errs, "pulseaudio not available")
	}
	return "", "", fmt.Errorf("audio sink: %s", strings.Join(errs, "; "))
}

// paRetry reports whether an open that failed with the libpulse error code
// may work when tried again: not when libpulse or the server refused the
// server or sink it was given, which no wait changes.
func paRetry(code int) bool {
	return code != C.PA_ERR_INVALID && code != C.PA_ERR_INVALIDSERVER
}

// audioServers returns the servers to try, in order: libpulse's default
// (""), which is PULSE_SERVER or client.conf's default-server when set and
// otherwise the user's socket (in PULSE_RUNTIME_PATH or XDG_RUNTIME_DIR),
// then the XDG_RUNTIME_DIR socket by name, so terminals with a stale
// PULSE_SERVER still get sound. When the default is that socket, trying it
// twice costs a failed connect, not a wait: openFirst tries every pair before
// it pauses.
func audioServers() []string {
	if socket := pulseSocketPath(); socket != "" {
		return []string{"", socket}
	}
	return []string{""}
}

// audioDevices returns the sink device candidates, in priority order. When
// SCT_AUDIO_DEVICE is set it is the only one; otherwise:
//  1. the default sink, when it is a sound card (alsa_output.*): what the
//     desktop's volume panel selects
//  2. the sinks other programs are playing on (they have sink-inputs)
//  3. the sound cards that are RUNNING
//  4. every sound card, in pactl's order
//  5. the server default (empty string), when pactl lists no sound card or
//     cannot run
//
// A default sink that is not a sound card (e.g. a game-streaming sink) may
// silently buffer audio nobody hears, so it is not taken first.
func audioDevices() []string {
	if d := envOr("SCT_AUDIO_DEVICE", ""); d != "" {
		return []string{d}
	}

	// 1) The desktop-selected default is the strongest signal of intent:
	//    it is what KDE's Audio Volume panel sets when you click a device.
	//    Prefer it (if physical; a virtual streaming sink default still
	//    means "route to what I'm using" - but only if it has streams).
	if def := pulseDefaultSink(); def != "" {
		if hasPrefix(def, "alsa_output.") {
			return []string{def}
		}
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

	// 2) active sinks (have streams), in pactl's order
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
	err := a.failure()
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v (check: pipewire or pulseaudio running? `pactl info`)", err)
}

func errNoAudio() error {
	return errPulseUnavailable{}
}

type errPulseUnavailable struct{}

func (errPulseUnavailable) Error() string {
	return "pulseaudio not available"
}

// cstrOrNil is C.CString, but NULL for "": libpulse takes NULL, not an empty
// string, for its default server or sink, and refuses "" as an invalid
// argument. Free the result with C.free.
func cstrOrNil(s string) *C.char {
	if s == "" {
		return nil
	}
	return C.CString(s)
}

// writePCM16 feeds interleaved s16 stereo samples to the sink (in place gain).
func (a *audioSink) writePCM16(pcm []byte) {
	if a.failure() != nil {
		return
	}
	if a.silent.Load() {
		return // web/window: the browser is the audio output (see silent)
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
		a.writeFailed.Store(true)
		logOnce("scterm: audio sink write failed; audio stopped. Check `pactl list sinks` and your audio server.\n")
	}
}

type errPulseLagged struct{}

func (errPulseLagged) Error() string { return "pulseaudio write failed" }

// setGain sets the volume in whole percent (0..200). Mute = 0, and the volume
// it replaces is kept for toggleMute.
func (a *audioSink) setGain(pct int) {
	a.volMu.Lock()
	defer a.volMu.Unlock()
	a.storeGain(pct)
}

// toggleMute mutes, or brings back the volume from before the mute (100% when
// there was none).
func (a *audioSink) toggleMute() {
	a.volMu.Lock()
	defer a.volMu.Unlock()
	if atomic.LoadInt32(&a.pct) > 0 {
		a.storeGain(0)
		return
	}
	pct := int(a.unmutePct)
	if pct == 0 {
		pct = 100
	}
	a.storeGain(pct)
}

// storeGain is setGain with volMu held.
func (a *audioSink) storeGain(pct int) {
	pct = min(max(pct, 0), 200)
	if old := atomic.LoadInt32(&a.pct); pct == 0 && old > 0 {
		a.unmutePct = old
	}
	atomic.StoreInt32(&a.pct, int32(pct))
	atomic.StoreInt32(&a.gain, int32(pct*256/100))
}

func (a *audioSink) gainPercent() int {
	return int(atomic.LoadInt32(&a.pct))
}

// cleanupStaleStreams: if scterm was killed hard (SIGKILL, pane death) or a
// stray instance is still alive in another pane, its sink-input keeps
// playing audio and no amount of quitting the visible instance stops it
// ("the app is killed but the sound still plays"). A session that plays its
// sound on this computer (playsHere) kills every leftover scterm sink-input
// at start, BEFORE opening its own stream, so it is the only audio source.
// A session that plays nothing here leaves them alone: they may belong to
// another live session. (pactl has no kill-sink-input; the libpulse control
// API does.)
func cleanupStaleStreams() {
	if n := killStaleStreams(audioServers(), killAppStreams); n > 0 {
		noticef("scterm: silenced %d leftover audio stream(s)\n", n)
	}
}

// killAppStreams kills the streams of app on server ("" is libpulse's
// default) and returns how many, or -1 when it cannot connect.
var killAppStreams = func(server, app string) int {
	cserver, capp := cstrOrNil(server), C.CString(app)
	defer C.free(unsafe.Pointer(cserver))
	defer C.free(unsafe.Pointer(capp))
	return int(C.sct_audio_kill_streams(cserver, capp))
}

// killStaleStreams runs kill on the first of servers that answers (open tries
// them in the same order) and returns how many streams it killed.
func killStaleStreams(servers []string, kill func(server, app string) int) int {
	for _, server := range servers {
		n := kill(server, "scterm")
		if n < 0 {
			continue
		}
		// Kill both the current and the legacy application name: a stray
		// stream from an old sct binary must not survive the rename.
		return n + max(kill(server, "sct"), 0)
	}
	return 0
}

func (a *audioSink) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sink != nil {
		C.sct_audio_close(a.sink)
		a.sink = nil
	}
}
