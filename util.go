package main

import (
	"fmt"
	"os"
	"sort"
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

// writePPMFile writes an RGBA buffer as a binary PPM to path (alpha ignored).
func writePPMFile(path string, rgba []byte, w, h int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "P6\n%d %d\n255\n", w, h)
	for y := 0; y < h; y++ {
		row := rgba[y*w*4 : (y+1)*w*4]
		for x := 0; x < w; x++ {
			f.Write(row[x*4 : x*4+3]) // ignore alpha
		}
	}
	return nil
}

// dumpPPM writes an RGBA canvas as a binary PPM (verification tool).
func dumpPPM(dir string, idx int, rgba []byte, w, h int) error {
	os.MkdirAll(dir, 0o755)
	return writePPMFile(fmt.Sprintf("%s/frame%d.ppm", dir, idx), rgba, w, h)
}

// printSortedKeys lists all Android keycodes (for -keys).
func printSortedKeys() {
	for _, v := range sortedKeys() {
		fmt.Fprintf(os.Stdout, "%3d %s\n", v, androidKeycodes[v])
	}
}

func sortedKeys() []int {
	var out []int
	for k := range androidKeycodes {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// maxAbsInt16 returns the maximum absolute s16 sample in b.
func maxAbsInt16(b []byte) int16 {
	var m int16
	for i := 0; i+1 < len(b); i += 2 {
		v := int16(b[i]) | int16(b[i+1])<<8
		if v < 0 {
			if -v > m {
				m = -v
			}
		} else if v > m {
			m = v
		}
	}
	return m
}

// overlayLine is one row of TUI overlay text with optional styling segments.
// hl = cursor (reverse video), fl = press flash (green background),
// segs = arbitrary SGR-colored spans (keyboard buttons) with rune offsets.
type overlaySeg struct {
	from, to int // rune-column offsets
	code     string
}

type overlayLine struct {
	text   string
	hlFrom int
	hlTo   int
	flFrom int
	flTo   int
	segs   []overlaySeg // keyboard buttons: emitted in order, non-overlapping
}

// runeByteIndex converts a rune-column offset into a byte offset of s.
// Returns -1 if the offset is past the end.
func runeByteIndex(s string, col int) int {
	if col <= 0 {
		return 0
	}
	count := 0
	for i := range s {
		if count == col {
			return i
		}
		count++
	}
	return -1
}
