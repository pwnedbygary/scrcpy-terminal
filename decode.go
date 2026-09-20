package main

/*
#cgo CFLAGS: -O3 -mavx2 -fomit-frame-pointer -I${SRCDIR}
#cgo pkg-config: libavcodec libavutil libswscale libswresample libpulse-simple
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

func avVersion() string {
	return C.GoString(C.sct_av_version())
}

// ---------------------------------------------------------------------------
// video
// ---------------------------------------------------------------------------

func vdecOpen(codecID, w, h int) unsafe.Pointer {
	return C.sct_vdec_open(C.int(codecID), C.int(w), C.int(h))
}

func vdecSend(d unsafe.Pointer, data []byte, ptsUs int64) error {
	if len(data) == 0 {
		return nil
	}
	r := C.sct_vdec_send(d, (*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.int(len(data)), C.int64_t(ptsUs))
	if r != 0 {
		return fmt.Errorf("vdec send failed")
	}
	return nil
}

// vdecRecv returns (hasFrame, err)
func vdecRecv(d unsafe.Pointer) (bool, error) {
	r := C.sct_vdec_recv(d)
	switch r {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, fmt.Errorf("vdec recv failed")
	}
}

// vdecScale scales the last decoded frame into rgba (w*2rows*4 bytes).
func vdecScale(d unsafe.Pointer, rgba []byte, w, h int) (fw, fh int, err error) {
	return vdecScaleStride(d, rgba, w*4, w, h)
}

// vdecScaleStride scales into dst of w x h pixels with an explicit stride
// (allows writing into a sub-region of a larger canvas).
func vdecScaleStride(d unsafe.Pointer, dst []byte, stride, w, h int) (fw, fh int, err error) {
	if len(dst) < (h-1)*stride+w*4 {
		panic("vdecScaleStride: dst too small")
	}
	var cfw, cfh C.int
	r := C.sct_vdec_scale(d, (*C.uint8_t)(unsafe.Pointer(&dst[0])),
		C.int(stride), C.int(w), C.int(h), &cfw, &cfh)
	if r != 0 {
		return 0, 0, fmt.Errorf("vdec scale failed")
	}
	return int(cfw), int(cfh), nil
}

func vdecFree(d unsafe.Pointer) {
	C.sct_vdec_free(d)
}

// ---------------------------------------------------------------------------
// audio
// ---------------------------------------------------------------------------

func adecOpen(codecID int) unsafe.Pointer {
	return C.sct_adec_open(C.int(codecID))
}

func adecIsRaw(codecID int) bool {
	return C.sct_adec_is_raw(C.int(codecID)) != 0
}

func adecSend(d unsafe.Pointer, data []byte, ptsUs int64) error {
	if len(data) == 0 {
		return nil
	}
	r := C.sct_adec_send(d, (*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.int(len(data)), C.int64_t(ptsUs))
	if r != 0 {
		return fmt.Errorf("adec send failed")
	}
	return nil
}

// adecRecv returns decoded interleaved s16 stereo bytes (may be empty).
func adecRecv(d unsafe.Pointer, buf []byte) (int, error) {
	var n C.int
	r := C.sct_adec_recv(d, (*C.uint8_t)(unsafe.Pointer(&buf[0])),
		C.int(len(buf)), &n)
	if r != 0 {
		return 0, fmt.Errorf("adec recv failed")
	}
	return int(n), nil
}

func adecFree(d unsafe.Pointer) {
	C.sct_adec_free(d)
}

// ---------------------------------------------------------------------------
// JPEG encoder (web/window display path)
// ---------------------------------------------------------------------------

// jencHandle wraps the native mjpeg encoder. A nil *jencHandle is safe to use:
// every method degrades to a no-op/error instead of crashing, which keeps the
// display path alive when libavcodec lacks an mjpeg encoder.
type jencHandle struct{ p unsafe.Pointer }

func jencOpen(w, h, quality int) *jencHandle {
	p := C.sct_jenc_open(C.int(w), C.int(h), C.int(quality))
	if p == nil {
		return nil
	}
	return &jencHandle{p: p}
}

// encode converts one BGR0 canvas (len = w*h*4) to JPEG. The returned slice
// aliases the encoder's internal buffer and is valid until the next encode.
// Prefer encodeInto on the network path: it writes straight into the buffer
// that goes to the socket, with no intermediate copy.
func (j *jencHandle) encode(bgr0 []byte) ([]byte, error) {
	if j == nil || j.p == nil {
		return nil, fmt.Errorf("jpeg encoder unavailable")
	}
	if len(bgr0) == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	var out *C.uint8_t
	var n C.int
	r := C.sct_jenc_encode(j.p, (*C.uint8_t)(unsafe.Pointer(&bgr0[0])), C.int(0), &out, &n)
	if r != 0 || out == nil || n <= 0 {
		return nil, fmt.Errorf("jpeg encode failed")
	}
	return C.GoBytes(unsafe.Pointer(out), n), nil
}

// maxSize is a safe upper bound for one encoded frame, used to size the
// header+payload buffer once per geometry instead of retrying per frame.
func (j *jencHandle) maxSize() int {
	if j == nil || j.p == nil {
		return 0
	}
	return int(C.sct_jenc_max_size(j.p))
}

// encodeInto writes the JPEG for one BGR0 canvas into dst[dstOff:].
// Returns the JPEG length, or -1 when dst was too small.
func (j *jencHandle) encodeInto(bgr0, dst []byte, dstOff int) (int, error) {
	if j == nil || j.p == nil {
		return 0, fmt.Errorf("jpeg encoder unavailable")
	}
	if len(bgr0) == 0 || len(dst) <= dstOff {
		return 0, fmt.Errorf("empty frame")
	}
	var n C.int
	r := C.sct_jenc_encode_to(j.p,
		(*C.uint8_t)(unsafe.Pointer(&bgr0[0])), C.int(0),
		(*C.uint8_t)(unsafe.Pointer(&dst[dstOff])), C.int(0),
		C.int(len(dst)-dstOff), &n)
	if r == -2 {
		return int(n), errJencTooSmall
	}
	if r != 0 || n <= 0 {
		return 0, fmt.Errorf("jpeg encode failed")
	}
	return int(n), nil
}

var errJencTooSmall = errors.New("jpeg output buffer too small")

func (j *jencHandle) free() {
	if j == nil || j.p == nil {
		return
	}
	C.sct_jenc_free(j.p)
	j.p = nil
}

// ---------------------------------------------------------------------------
// cells & gain
// ---------------------------------------------------------------------------

// packCells fills keys with quantized half-block cells from an RGBA canvas of
// w x h pixels (h even), unless the buffers don't line up (resize race).
// Never panics: returns false on mismatch, caller drops the frame.
func packCells(rgba []byte, keys []uint64, w, h int) bool {
	if len(rgba) != w*h*4 || len(keys) != w*(h/2) {
		return false
	}
	C.sct_pack_cells((*C.uint8_t)(unsafe.Pointer(&rgba[0])), C.int(w), C.int(h),
		(*C.uint64_t)(unsafe.Pointer(&keys[0])))
	return true
}

// gainS16 applies fixed-point q8 gain (256 = 1.0, 0 = mute) in place.
func gainS16(buf []int16, gain int32) {
	if gain == 256 {
		return
	}
	if len(buf) == 0 {
		return
	}
	C.sct_gain_s16((*C.int16_t)(unsafe.Pointer(&buf[0])),
		C.int(len(buf)), C.int(gain))
}
