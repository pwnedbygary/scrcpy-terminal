package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"testing"
	"unsafe"
)

// ---------------------------------------------------------------------------
// The terminal display scales each frame into the picture's region of the
// canvas, and the letterbox bars around it stay black. libswscale's vector
// code converts whole blocks of pixels: on a row whose width is not a
// multiple of the block it wrote past the picture, into the bars, and, at the
// frame's own size, past the end of a buffer that ends with the picture.
// These tests run the real decoder on the screenshot fixture and on two small
// frames of its picture.
// ---------------------------------------------------------------------------

// scaleMark fills the bytes around the picture. No pixel the scaler writes is
// four of them: the fourth byte of a BGR0 pixel is 0 or 255.
const scaleMark = 0xa5

// decoderHolding is a decoder that holds the frame of a w x h stream that
// data decodes to: Annex B, the SPS and PPS in front of the frame.
func decoderHolding(t *testing.T, w, h int, data []byte) unsafe.Pointer {
	t.Helper()
	dec := vdecOpen(codecH264, w, h)
	if dec == nil {
		t.Fatal("could not open the H.264 decoder")
	}
	t.Cleanup(func() { vdecFree(dec) })
	if err := vdecSend(dec, data, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := vdecRecv(dec); !ok || err != nil {
		t.Fatalf("the frame was not decoded (%v)", err)
	}
	return dec
}

// fixtureDecoder is a decoder that holds the first frame of the screenshot
// fixture: 1280x2800, red, green and blue quarters on dark blue.
func fixtureDecoder(t *testing.T) unsafe.Pointer {
	t.Helper()
	config, frames := fixturePackets(t)
	// The payloads without their 12-byte headers, as runVideo sends them.
	return decoderHolding(t, 1280, 2800, slices.Concat(config[12:], frames[0][12:]))
}

// markedBuffer is n bytes of scaleMark.
func markedBuffer(n int) []byte { return bytes.Repeat([]byte{scaleMark}, n) }

// writtenBytes is how far into b the last byte that lost its mark is: 0 when
// every byte has it.
func writtenBytes(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != scaleMark {
			return i + 1
		}
	}
	return 0
}

// checkScaled checks buf after the frame was scaled to w x h into it, from
// byte off on, with rows stride bytes apart: every byte outside the picture
// keeps its mark, every pixel of the picture was written, and four columns of
// the picture have the fixture's colours in every row but those near the
// middle, where its quarters meet.
func checkScaled(t *testing.T, buf []byte, off, stride, w, h int) {
	t.Helper()
	if n := writtenBytes(buf[:off]); n > 0 {
		t.Errorf("wrote before the picture: up to byte %d of %d", n, off)
	}
	// Right of each row of the picture: up to the next row, or to the end of
	// buf after the last.
	rows, most := 0, 0
	for y := range h {
		from, to := off+y*stride+w*4, len(buf)
		if y < h-1 {
			to = off + (y+1)*stride
		}
		if n := writtenBytes(buf[from:to]); n > 0 {
			rows++
			most = max(most, n)
		}
	}
	if rows > 0 {
		t.Errorf("wrote right of the picture on %d of its %d rows, up to %d bytes (%d pixels)",
			rows, h, most, (most+3)/4)
	}

	unwritten := 0
	for y := range h {
		row := buf[off+y*stride:][:w*4]
		for x := range w {
			if binary.LittleEndian.Uint32(row[x*4:]) == scaleMark*0x01010101 {
				unwritten++
			}
		}
	}
	if unwritten > 0 {
		t.Errorf("%d of the picture's %d pixels were not written", unwritten, w*h)
	}

	if w < 16 || h < 16 {
		return // too few pixels for each quarter to keep its colour
	}
	// Both edges of the picture and the middle of each quarter, from the
	// picture's first row to its last, but for the rows within an eighth of
	// its height of the middle, where the quarters above meet those below and
	// the scaler blends them. (A copy a row off fills the first or the last
	// row from outside the scaled picture.) The H.264 round trip and the
	// scaler shift a colour by up to 16 levels here; another libswscale may
	// round differently. These colours are 96 or more apart in some channel,
	// and as far from black, so a pixel from a sheared row or another quarter,
	// a black one, or one the scaler never wrote, is off by more than 32.
	red, green, blue := [3]int{255, 0, 0}, [3]int{0, 255, 0}, [3]int{0, 0, 255}
	top, bottom := [2]int{0, 3 * h / 8}, [2]int{5 * h / 8, h - 1}
	for _, c := range []struct {
		x    int
		rows [2]int
		want [3]int
	}{
		{0, top, red}, {w / 4, top, red}, {3 * w / 4, top, green}, {w - 1, top, green},
		{0, bottom, blue}, {w / 4, bottom, blue}, {3 * w / 4, bottom, darkBlue}, {w - 1, bottom, darkBlue},
	} {
	column:
		for y := c.rows[0]; y <= c.rows[1]; y++ {
			px := buf[off+y*stride+c.x*4:] // B, G, R, unused
			got := [3]int{int(px[2]), int(px[1]), int(px[0])}
			for i := range got {
				if d := got[i] - c.want[i]; d < -32 || d > 32 {
					t.Errorf("pixel (%d,%d) is %v, want about %v", c.x, y, got, c.want)
					break column
				}
			}
		}
	}
}

// TestScaleWritesOnlyThePicture: scaled into its region of a wider canvas, as
// the terminal display letterboxes it, the picture fills its region and the
// scaler writes no byte of the bars, whatever its width. A 576x1280 stream
// made pictures 20, 26 and 34 pixels wide in terminals of 80x24, 80x30 and
// 120x40, and the bars right of them showed junk colours; at 24 and 48, the
// multiples of 8, they did not.
func TestScaleWritesOnlyThePicture(t *testing.T) {
	dec := fixtureDecoder(t)
	const h, bar = 46, 24 // the picture's height in an 80x24 terminal; each bar's width
	for w := 2; w <= 64; w += 2 {
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			stride := (bar + w + bar) * 4
			buf := markedBuffer(stride * h)
			if _, _, err := vdecScaleStride(dec, buf[bar*4:], stride, w, h); err != nil {
				t.Fatal(err)
			}
			checkScaled(t, buf, bar*4, stride, w, h)
		})
	}
}

// TestScaleKeepsUpWithTheSize: one decoder scales to the size each call asks,
// as the video goroutine's does through resizes and screenshots: taller at
// the same width (a terminal a row taller can keep the picture's width), then
// wider at the same height, then the first size again.
func TestScaleKeepsUpWithTheSize(t *testing.T) {
	dec := fixtureDecoder(t)
	const bar = 24
	for _, size := range [][2]int{{20, 46}, {20, 110}, {34, 110}, {20, 46}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			stride := (bar + w + bar) * 4
			buf := markedBuffer(stride * h)
			if _, _, err := vdecScaleStride(dec, buf[bar*4:], stride, w, h); err != nil {
				t.Fatal(err)
			}
			checkScaled(t, buf, bar*4, stride, w, h)
		})
	}
}

// TestScaleStopsAtTheEndOfTheBuffer: a picture that reaches the right edge of
// the canvas ends its last row at the end of the buffer the scaler is given,
// which is all the scaler may write: here that buffer is the start of a
// larger one, whose bytes after it keep their mark. Scaled pictures as wide
// as the canvas, which a landscape picture is in a terminal whose width is
// not a multiple of 8, one at the right edge of a wider row, and the whole
// frame at its own size, 1280 wide. On the computer tested, the old code,
// which scaled straight into the buffer, wrote past its end in none of these
// cases (a scaled picture spilled on every row but its last two, and 1280 is
// a multiple of 16): it failed only at the right edge of the row, by spilling
// into the next row's bar. TestScaleAtTheFramesOwnSize shows it writing past
// the end, at 1080 wide.
func TestScaleStopsAtTheEndOfTheBuffer(t *testing.T) {
	dec := fixtureDecoder(t)
	const guard = 64
	for _, tc := range []struct {
		name          string
		canvasW, w, h int
	}{
		{"as wide as the canvas", 106, 106, 46},
		{"as wide as a narrow canvas", 20, 20, 46},
		{"at the right edge of the row", 120, 34, 78},
		{"a multiple of 8 at the right edge", 80, 24, 54},
		{"the whole frame", 1280, 1280, 2800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stride, off, end := tc.canvasW*4, (tc.canvasW-tc.w)*4, tc.canvasW*tc.h*4
			buf := markedBuffer(end + guard)
			if _, _, err := vdecScaleStride(dec, buf[off:end], stride, tc.w, tc.h); err != nil {
				t.Fatal(err)
			}
			if n := writtenBytes(buf[end:]); n > 0 {
				t.Errorf("wrote %d bytes past the end of the %dx%d picture's buffer", n, tc.w, tc.h)
			}
			checkScaled(t, buf, off, stride, tc.w, tc.h)
		})
	}
}

// TestScaleAtTheFramesOwnSize: a screenshot, --dump-frames and the browser
// display scale the frame at its own size, which libswscale converts with
// other vector code: on the computer tested (libswscale 10.1.102 on a
// Ryzen 7 7800X3D), 16 pixels at a time. In a buffer of the frame's own size,
// as a screenshot's and the browser canvas are, it wrote past the end of the
// buffer at 1080 wide, and left the last 2 pixels of each row unwritten at
// 34. In such a buffer, and in a wider canvas, as the terminal
// letterboxes a picture smaller than itself, the frame fills its region and
// nothing else.
func TestScaleAtTheFramesOwnSize(t *testing.T) {
	const guard, bar = 64, 24
	for _, tc := range []struct {
		w, h int
		data []byte
	}{
		{1080, 32, frame1080x32},
		{34, 32, frame34x32},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			dec := decoderHolding(t, tc.w, tc.h, tc.data)
			for _, layout := range []struct {
				name    string
				canvasW int
			}{
				{"in a buffer of its size", tc.w},
				{"in a wider canvas", bar + tc.w + bar},
			} {
				t.Run(layout.name, func(t *testing.T) {
					stride, off := layout.canvasW*4, (layout.canvasW-tc.w)/2*4
					end := off + (tc.h-1)*stride + tc.w*4 // where the picture's last row ends
					buf := markedBuffer(layout.canvasW*tc.h*4 + guard)
					fw, fh, err := vdecScaleStride(dec, buf[off:end], stride, tc.w, tc.h)
					if err != nil {
						t.Fatal(err)
					}
					if fw != tc.w || fh != tc.h {
						t.Fatalf("the frame is %dx%d, want %dx%d", fw, fh, tc.w, tc.h)
					}
					if n := writtenBytes(buf[end:]); n > 0 {
						t.Errorf("wrote %d bytes past the end of the buffer", n)
					}
					checkScaled(t, buf, off, stride, tc.w, tc.h)
				})
			}
		})
	}
}

// frame1080x32 and frame34x32 are the screenshot fixture's first picture at
// 1080x32 and 34x32, one frame each, with its SPS and PPS. FFmpeg 9.0.2 with
// x264 0.165.r3222 made them, and makes the same bytes again, with:
//
//	ffmpeg -f lavfi -i "color=c=0x204060:s=1080x32:r=10:d=0.1,drawbox=x=0:y=0:w=iw/2:h=ih/2:c=red:t=fill,drawbox=x=iw/2:y=0:w=iw/2:h=ih/2:c=lime:t=fill,drawbox=x=0:y=ih/2:w=iw/2:h=ih/2:c=blue:t=fill" \
//	  -frames:v 1 -c:v libx264 -profile:v high -preset veryslow -tune zerolatency -threads 1 -bf 0 -g 100 \
//	  -pix_fmt yuv420p -bitexact -bsf:v filter_units=remove_types=6 -f h264 1080x32.h264
//
// and the same with s=34x32.
var (
	frame1080x32 = []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x15, 0xac, 0x4c, 0x22, 0x02, 0x22, 0xf2, 0xf8, 0x08,
		0x80, 0x00, 0x00, 0x03, 0x00, 0x80, 0x00, 0x00, 0x0a, 0x47, 0x8b, 0x17, 0x08, 0xc0, 0x00, 0x00,
		0x00, 0x01, 0x68, 0xe8, 0x43, 0x0f, 0x2c, 0x8b, 0x00, 0x00, 0x01, 0x65, 0x88, 0x82, 0x02, 0x5f,
		0xfe, 0xe8, 0xc9, 0xfc, 0xcb, 0x2b, 0x2f, 0xe3, 0xd4, 0x37, 0xb3, 0x46, 0x29, 0xb3, 0x49, 0xa7,
		0x1c, 0xc2, 0x3d, 0x1d, 0x30, 0xea, 0xf5, 0x1c, 0xa3, 0xe4, 0x36, 0x8c, 0x45, 0xe5, 0x11, 0x0c,
		0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x05, 0x45, 0x78, 0x07, 0xcf, 0x62, 0xa0, 0x02, 0x7c,
		0xdd, 0xc4, 0x7b, 0x24, 0xf7, 0xa5, 0xd4, 0x7f, 0xe4, 0x3d, 0x5b, 0x70, 0xe1, 0x09, 0x34, 0xf0,
		0xef, 0xad, 0xd8, 0x26, 0xfa, 0xf9, 0xed, 0x4b, 0x8d, 0xf2, 0xb8, 0x00, 0x00, 0x03, 0x02, 0x94,
		0x19, 0x04, 0xa2, 0x99, 0x7d, 0xbe, 0xaa, 0x7e, 0xcf, 0xab, 0xc7, 0xff, 0xd7, 0x18, 0xed, 0x92,
		0xf7, 0x41, 0x94, 0xdf, 0x78, 0x03, 0x67, 0x3a, 0xe1, 0x06, 0x80, 0x00, 0x00, 0x03, 0x00, 0x34,
		0xe4, 0x4a, 0x63, 0x96, 0x61, 0xb4, 0xfc, 0x6f, 0xa1, 0xf8, 0x5c, 0xb1, 0xb6, 0xa1, 0x33, 0x09,
		0xf6, 0x86, 0x34, 0x90, 0x00, 0x08, 0x19,
	}
	frame34x32 = []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x0a, 0xac, 0x4c, 0x22, 0x6b, 0xc4, 0x78, 0x08, 0x80,
		0x00, 0x00, 0x03, 0x00, 0x80, 0x00, 0x00, 0x0a, 0x47, 0x89, 0x13, 0x08, 0xc0, 0x00, 0x00, 0x00,
		0x01, 0x68, 0xe8, 0x43, 0x0f, 0x2c, 0x8b, 0x00, 0x00, 0x01, 0x65, 0x88, 0x82, 0x02, 0x5f, 0xfe,
		0xe8, 0xc9, 0xfc, 0xcb, 0x2b, 0x2f, 0xe3, 0xd4, 0x37, 0xb3, 0x46, 0x29, 0xb1, 0x1a, 0x1b, 0xe1,
		0x72, 0x7e, 0xf4, 0x00, 0xb1, 0x85, 0xe4, 0xfb, 0xba, 0xa3, 0x9b, 0xc4, 0xac, 0xe2, 0x33, 0xc5,
		0xad, 0xff, 0xf9, 0x87, 0x9b, 0x98, 0xac, 0xa5, 0x14, 0xa6, 0xe5, 0xab, 0x6d, 0x48, 0x66, 0x2b,
		0xe1, 0xca, 0xd1, 0xea, 0xea, 0x6b, 0xb7, 0xad, 0xc8, 0xd7, 0x20, 0x41, 0x49, 0xed, 0x05, 0x3e,
		0xd9, 0x89, 0x58, 0xc1, 0xe5, 0xb6, 0x42, 0xf1, 0x59, 0x6d, 0x97, 0xff,
	}
)
