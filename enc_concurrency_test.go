package main

// Concurrent encoding on one shared encoder.
//
// Every client now shares a single canvas (the video's own geometry), so every
// client resolves to the SAME pooled jpegEncoder. A libavcodec encoder context
// is not thread-safe: two client goroutines inside one AVCodecContext at the
// same time corrupts it and faults inside the native encoder (observed as
// SIGSEGV in sct_jenc_encode_to with addr=0x0 and a correctly-sized input
// buffer, i.e. not the frame-length bug).
//
// Note the Go race detector cannot see this: the race is inside C.

import (
	"sync"
	"testing"
)

func TestEncodeWithConcurrentClients(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, video: true}, false)

	// One geometry: what several clients get when they share the canvas.
	cv := computeWebCanvas(1280, 720, 1280, 720)
	src := makeCanvas(cv.cw, cv.ch, 9)

	const clients = 8
	const frames = 40

	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			size := h.srv.encoderMaxSize(cv)
			if size <= 0 {
				t.Errorf("client %d: no encoder for %dx%d", id, cv.cw, cv.ch)
				return
			}
			dst := make([]byte, webHeaderLen+size)
			for j := 0; j < frames; j++ {
				n, err := h.srv.encodeWith(cv, src, dst, webHeaderLen)
				if err != nil {
					t.Errorf("client %d frame %d: %v", id, j, err)
					return
				}
				if n <= 0 {
					t.Errorf("client %d frame %d: encoded %d bytes", id, j, n)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
