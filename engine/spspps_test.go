package engine

import "testing"

func TestParseSPSPPS(t *testing.T) {
	// Real CONFIG payload captured from Retroid Pocket 6:
	//   00 00 00 01 67 64 00 20 ac b4 02 80 2d d3 50 00 60 00 6d 0a 13 50
	//   00 00 00 01 68 ee 06 f2 c0
	config := []byte{
		0x00, 0x00, 0x00, 0x01,
		0x67, 0x64, 0x00, 0x20, 0xac, 0xb4, 0x02, 0x80, 0x2d, 0xd3, 0x50, 0x00, 0x60, 0x00, 0x6d, 0x0a, 0x13, 0x50,
		0x00, 0x00, 0x00, 0x01,
		0x68, 0xee, 0x06, 0xf2, 0xc0,
	}
	sps, pps := ParseSPSPPS(config)
	if len(sps) == 0 {
		t.Fatal("no SPS found")
	}
	if len(pps) == 0 {
		t.Fatal("no PPS found")
	}
	if sps[0]&0x1F != 7 {
		t.Errorf("SPS NAL type = %d, want 7", sps[0]&0x1F)
	}
	if pps[0]&0x1F != 8 {
		t.Errorf("PPS NAL type = %d, want 8", pps[0]&0x1F)
	}
	t.Logf("SPS: %d bytes (head %x)", len(sps), sps[:min(8, len(sps))])
	t.Logf("PPS: %d bytes (head %x)", len(pps), pps[:min(8, len(pps))])

	// Dimensions: this SPS is for 1280x720 (the Retroid Pocket 6 stream at
	// max_size=1280). High profile (0x64), level 3.2 (0x20).
	w, h := ExtractSPSDimensions(sps)
	t.Logf("SPS dims: %dx%d", w, h)
	if w != 1280 || h != 720 {
		t.Errorf("dims = %dx%d, want 1280x720", w, h)
	}
}
