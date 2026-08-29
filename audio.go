package main

/*
#cgo CFLAGS: -O3 -mavx2 -fomit-frame-pointer -I${SRCDIR}
#cgo pkg-config: libpulse-simple
#include <pulse/simple.h>
#include <pulse/error.h>
#include <stdlib.h>

// audioSink wraps pa_simple.
typedef struct {
    pa_simple *s;
    int err;
    int valid;
} sct_audio_sink;

static sct_audio_sink *sct_audio_open(const char *app_name) {
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
    a->s = pa_simple_new(NULL, app_name, PA_STREAM_PLAYBACK, NULL,
                         "sct audio", &ss, NULL, &attr, &a->err);
    a->valid = a->s != NULL;
    return a;
}

static int sct_audio_write(sct_audio_sink *a, const uint8_t *data, int len) {
    if (!a || !a->valid) return -1;
    if (pa_simple_write(a->s, data, (size_t) len, &a->err) < 0) return -1;
    return 0;
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
	"sync"
	"sync/atomic"
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
	sink := C.sct_audio_open(cstr("sct"))
	if sink == nil || sink.valid == 0 {
		a.err = errNoAudio()
		if sink != nil {
			C.sct_audio_close(sink)
		}
	} else {
		a.sink = sink
	}
	return a
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
