package main

// Benchmarks for the web/window display path's per-frame encode cost.
//
// Two geometries matter in practice:
//
//	1777x1000 -- the default --window size on a 1920x1080-rotated device,
//	             which is what a maximised-ish window actually encodes.
//	 480x1080 -- the default --web canvas.
//
// The input canvas is deliberately NOISE-HEAVY: a gradient plus per-pixel
// random detail is much harder to compress than real screen content (flat
// backgrounds, text, UI chrome), so these numbers are a pessimistic bound.
// The authoritative figure for real content is the live EWMA reported as
// jpegMs in the status message.
//
// Run: go test -run XXX -bench Encode -benchtime 200x

import (
	"math/rand"
	"testing"
)

// benchCanvas builds a BGR0 canvas with screen-like-but-hard content.
func benchCanvas(w, h int) []byte {
	rgb := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			// Vertical gradient + horizontal banding + fine noise: the noise
			// term is what stops the mjpeg encoder from trivially winning.
			g := uint8((y * 255 / h) ^ (x / 8))
			rgb[i+0] = g ^ uint8(rng.Intn(32))
			rgb[i+1] = g
			rgb[i+2] = uint8(x * 255 / w)
			rgb[i+3] = 0
		}
	}
	return rgb
}

func benchEncode(b *testing.B, w, h, quality int) {
	hnd := jencOpen(w, h, quality)
	if hnd == nil {
		b.Skip("no mjpeg encoder available")
	}
	defer hnd.free()

	src := benchCanvas(w, h)
	dst := make([]byte, hnd.maxSize())
	if len(dst) == 0 {
		b.Fatal("encoder reported max size 0")
	}

	// One warm-up encode so the first-call cost is not in the measurement.
	if n, err := hnd.encodeInto(src, dst, 0); err != nil || n <= 0 {
		b.Fatalf("warm-up encode failed: n=%d err=%v", n, err)
	}

	b.SetBytes(int64(w * h * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := hnd.encodeInto(src, dst, 0)
		if err != nil || n <= 0 {
			b.Fatalf("encode failed: n=%d err=%v", n, err)
		}
	}
	b.StopTimer()
	// Report the encoded size once, outside the timed region.
	if n, err := hnd.encodeInto(src, dst, 0); err == nil && n > 0 {
		b.ReportMetric(float64(n)/1024, "KB/frame")
	}
}

func BenchmarkEncodeWeb1280x720(b *testing.B) {
	benchEncode(b, 1280, 720, webDefaultQuality)
}

func BenchmarkEncodeWindow1777x1000(b *testing.B) {
	benchEncode(b, 1777, 1000, webDefaultQuality)
}

func BenchmarkEncodeWeb480x1080(b *testing.B) {
	benchEncode(b, 480, 1080, webDefaultQuality)
}

func BenchmarkEncodeWindowBestQuality(b *testing.B) {
	benchEncode(b, 1777, 1000, webMinQuality)
}
