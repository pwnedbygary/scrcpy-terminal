package main

import (
	"fmt"
	"os"
	"time"
	"unsafe"
)

func timeNowUnixNano() int64 { return time.Now().UnixNano() }

func int16Slice(b []byte) []int16 {
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}

func bytePtr(b []byte) unsafe.Pointer {
	return unsafe.Pointer(&b[0])
}

func writeStdout(b []byte) {
	os.Stdout.Write(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// truncateRunes cuts s to at most n runes (safe for wide multibyte text).
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// dumpPPM writes an RGBA canvas as a binary PPM (verification tool).
func dumpPPM(dir string, idx int, rgba []byte, w, h int) error {
	os.MkdirAll(dir, 0o755)
	fn := fmt.Sprintf("%s/frame%d.ppm", dir, idx)
	f, err := os.Create(fn)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "P6\n%d %d\n255\n", w, h)
	for y := 0; y < h; y++ {
		row := rgba[y*w*4 : (y+1)*w*4]
		for x := 0; x < w; x++ {
			px := row[x*4 : x*4+4]
			f.Write(px[0:3]) // ignore alpha
		}
	}
	return nil
}
