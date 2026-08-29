package main

/*
#cgo CFLAGS: -O3 -mavx2 -fomit-frame-pointer -I${SRCDIR}
#cgo pkg-config: libavcodec libavutil libswscale libswresample libpulse-simple
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
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
