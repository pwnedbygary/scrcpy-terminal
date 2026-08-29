package main

import (
	"encoding/binary"
	"os"
	"testing"
)

// Verify the real wire capture decodes to non-silence through libavcodec.
func TestWireDecode(t *testing.T) {
	data, err := os.ReadFile("/tmp/aud.wire")
	if err != nil {
		t.Skip("no wire dump")
	}
	dec := adecOpen(codecOpus)
	if dec == nil {
		t.Fatal("cannot open opus decoder")
	}
	defer adecFree(dec)
	off := 0
	total := 0
	peak := int16(0)
	pcm := make([]byte, 1<<16)
	for off+4 <= len(data) && total < 1000 {
		sz := int(binary.BigEndian.Uint32(data[off : off+4]))
		off += 4
		if off+sz > len(data) {
			break
		}
		payload := data[off : off+sz]
		off += sz
		if adecSend(dec, payload, int64(total*20000)) != nil {
			continue
		}
		for {
			n, err := adecRecv(dec, pcm)
			if err != nil || n == 0 {
				break
			}
			if pk := maxAbsInt16(pcm[:n]); pk > peak {
				peak = pk
			}
			total++
		}
	}
	if peak == 0 {
		t.Fatalf("decode produced only silence")
	}
	t.Logf("decoded %d frames, peak=%d (NON-SILENT)", total, peak)
}
