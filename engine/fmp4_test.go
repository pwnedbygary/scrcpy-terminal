package engine

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
)

// TestFMP4InitSegmentBoxOrder checks the top-level structure of the init
// segment: ftyp + moov, with the required sub-boxes.
func TestFMP4InitSegmentBoxOrder(t *testing.T) {
	m := NewFMP4Muxer()
	// Minimal fake SPS/PPS — the body needs at least 4 bytes for profile/level.
	m.SetSPSPPS([]byte{0x67, 0x42, 0x00, 0x1e, 0xab, 0xcd, 0xef, 0x12}, // SPS
		[]byte{0x68, 0xce, 0x38, 0x80},    // PPS
		1280, 720)

	var buf bytes.Buffer
	if err := m.WriteInit(&buf); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()

	// Top level: ftyp (8+4+4+4*N), moov (8+...)
	if got[4] != 'f' || got[5] != 't' || got[6] != 'y' || got[7] != 'p' {
		t.Fatalf("first box not ftyp: %q", got[4:8])
	}
	ftypSize := binary.BigEndian.Uint32(got[0:4])
	if int(ftypSize) >= len(got) {
		t.Fatalf("ftyp size %d overruns buffer %d", ftypSize, len(got))
	}
	rest := got[ftypSize:]
	if rest[4] != 'm' || rest[5] != 'o' || rest[6] != 'o' || rest[7] != 'v' {
		t.Fatalf("second box not moov: %q", rest[4:8])
	}
	t.Logf("init: ftyp=%d + moov=%d = %d bytes", ftypSize, binary.BigEndian.Uint32(rest[0:4]), len(got))
}

// TestFMP4FragmentBoxLayout checks one moof+mdat fragment round-trips.
// The trun's data_offset must point to the start of the sample data
// inside mdat.
func TestFMP4FragmentBoxLayout(t *testing.T) {
	m := NewFMP4Muxer()
	m.SetSPSPPS([]byte{0x67, 0x42, 0x00, 0x1e, 0xab, 0xcd, 0xef, 0x12},
		[]byte{0x68, 0xce, 0x38, 0x80},
		1280, 720)

	var buf bytes.Buffer
	// 33ms keyframe
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, 0x33, 0xff, 0xff, 0xff}
	if err := m.WriteFrame(&buf, true, 33_000, annexB); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	// First box: moof
	if got[4] != 'm' || got[5] != 'o' || got[6] != 'o' || got[7] != 'f' {
		t.Fatalf("first box not moof: %q", got[4:8])
	}
	moofSize := int(binary.BigEndian.Uint32(got[0:4]))
	if moofSize >= len(got) {
		t.Fatalf("moof overruns: %d vs %d", moofSize, len(got))
	}
	// second box: mdat
	mdatHdr := got[moofSize:]
	if mdatHdr[4] != 'm' || mdatHdr[5] != 'd' || mdatHdr[6] != 'a' || mdatHdr[7] != 't' {
		t.Fatalf("second box not mdat: %q", mdatHdr[4:8])
	}
	// data_offset inside trun is at offset 80+16 inside moof (see fmp4.go)
	dataOffset := binary.BigEndian.Uint32(got[80+16:])
	if int(dataOffset) != moofSize {
		t.Errorf("trun data_offset=%d, want %d (moof size)", dataOffset, moofSize)
	}
	t.Logf("frame: moof=%d + mdat=%d = %d bytes; data_offset=%d",
		moofSize, binary.BigEndian.Uint32(mdatHdr[0:4]), len(got), dataOffset)
}

// TestFMP4WithFFProbe runs the init + a few fragments through ffprobe
// to verify the muxer produces a structurally valid ISO-BMFF stream.
// Skipped if ffprobe is not in PATH.
func TestFMP4WithFFProbe(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	m := NewFMP4Muxer()
	m.SetSPSPPS(
		[]byte{0x67, 0x42, 0x00, 0x1e, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56},
		[]byte{0x68, 0xce, 0x38, 0x80},
		1280, 720)

	var buf bytes.Buffer
	if err := m.WriteInit(&buf); err != nil {
		t.Fatal(err)
	}
	// 3 frames at 30fps: 0, 33ms, 66ms
	for i, pts := range []uint64{0, 33_000, 66_000} {
		annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, 0x33, 0xff, 0xff, 0xff}
		_ = annexB
		// vary the size slightly
		annexB = append(annexB, make([]byte, 100+i)...)
		if err := m.WriteFrame(&buf, i == 0, pts, annexB); err != nil {
			t.Fatal(err)
		}
	}

	tmp, err := os.CreateTemp("", "scterm-fmp4-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	out, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", tmp.Name()).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe error: %v\n%s", err, out)
	}
	t.Logf("ffprobe OK:\n%s", out)
	if !bytes.Contains(out, []byte("h264")) {
		t.Errorf("ffprobe didn't see h264 codec: %s", out)
	}
}

// TestAnnexBToLengthPrefixed: small known input.
func TestAnnexBToLengthPrefixed(t *testing.T) {
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1e, // SPS NAL = 4 bytes
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80, // PPS NAL = 4 bytes
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84,       // IDR NAL = 3 bytes
	}
	out := annexBToLengthPrefixed(annexB)
	want := []byte{
		0, 0, 0, 4, 0x67, 0x42, 0x00, 0x1e,
		0, 0, 0, 4, 0x68, 0xce, 0x38, 0x80,
		0, 0, 0, 3, 0x65, 0x88, 0x84,
	}
	if !bytes.Equal(out, want) {
		t.Errorf("got %x want %x", out, want)
	}
}
