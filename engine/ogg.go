// Ogg-Opus muxer for the engine's raw Opus packets (RFC 7845).
// Replaces ffmpeg's `-c:a copy -f ogg` in the hot path.
package engine

import (
	"encoding/binary"
	"io"
)

// OggMuxer writes a live OggOpus stream from raw Opus packets.
type OggMuxer struct {
	w        io.Writer
	serial   uint32
	seq      uint32
	channels byte
	preSkip  uint16
	granule  int64
	started  bool
}

func NewOggMuxer(w io.Writer, channels int) *OggMuxer {
	return &OggMuxer{w: w, serial: 0x5343544d, channels: byte(channels), preSkip: 312}
}

// WriteHeaders emits the BOS page (OpusHead) + OpusTags page.
func (m *OggMuxer) WriteHeaders() error {
	head := []byte("OpusHead")
	head = append(head, 1) // version
	head = append(head, m.channels)
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], m.preSkip)
	head = append(head, b[:]...)
	var s [4]byte
	binary.LittleEndian.PutUint32(s[:], 48000)
	head = append(head, s[:]...)
	head = append(head, 0, 0) // output gain
	head = append(head, 0)    // mapping family
	if err := m.writePacket(head, true, false); err != nil {
		return err
	}
	tags := []byte("OpusTags")
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], 8)
	tags = append(tags, v[:]...)
	tags = append(tags, "scterm"...)
	var c [4]byte
	binary.LittleEndian.PutUint32(c[:], 0)
	tags = append(tags, c[:]...)
	return m.writePacket(tags, false, false)
}

// WritePacket muxes one Opus packet into Ogg pages.
func (m *OggMuxer) WritePacket(data []byte) error {
	m.granule += 960 // 20 ms @ 48 kHz
	return m.writePacket(data, false, false)
}

// writePacket segments the packet and emits pages (BOS only on the header).
func (m *OggMuxer) writePacket(pkt []byte, bos bool, eos bool) error {
	// lacing values: ceil-divided 255s, with a trailing 0 if the packet
	// ends exactly on a 255 boundary
	segs := make([]byte, 0, len(pkt)/255+2)
	rem := len(pkt)
	for rem >= 255 {
		segs = append(segs, 255)
		rem -= 255
	}
	if rem > 0 || (len(pkt) > 0 && len(pkt)%255 == 0) {
		segs = append(segs, byte(rem))
	}
	if len(pkt) == 0 {
		segs = []byte{0}
	}

	// one page holds this packet (small frames); if a packet ever
	// exceeds 255 segments, split into multiple pages — opus frames are
	// ≤ 1275 bytes (5*255) so a single page always suffices here.
	page := make([]byte, 0, 27+len(segs)+len(pkt))
	page = append(page, 'O', 'g', 'g', 'S', 0, 0) // magic, version, header type
	if bos {
		page[5] = 0x02
	}
	{
		var g [8]byte
		granule := m.granule
		if bos {
			granule = 0
		}
		binary.LittleEndian.PutUint64(g[:], uint64(granule))
		page = append(page, g[:]...)
	}
	var serial [4]byte
	binary.LittleEndian.PutUint32(serial[:], m.serial)
	page = append(page, serial[:]...)
	var seq [4]byte
	binary.LittleEndian.PutUint32(seq[:], m.seq)
	page = append(page, seq[:]...)
	page = append(page, 0, 0, 0, 0) // CRC (filled below)
	page = append(page, byte(len(segs)))
	page = append(page, segs...)
	page = append(page, pkt...)
	m.seq++

	crc := oggCRC(page)
	// the CRC field is little-endian in the page header (like every other
	// multi-byte field); writing it BE makes ffmpeg report "CRC mismatch"
	page[22] = byte(crc)
	page[23] = byte(crc >> 8)
	page[24] = byte(crc >> 16)
	page[25] = byte(crc >> 24)
	_, err := m.w.Write(page)
	return err
}

// oggCRC: libogg's CRC-32 (poly 0x04c11db7, init 0, MSB-first, no xor).
func oggCRC(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

var _ = binary.BigEndian