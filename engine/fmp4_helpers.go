// fmp4_helpers.go — small ISO-BMFF box helpers used by fmp4.go.
package engine

import "encoding/binary"

// u32 / u16 / u64 append big-endian.
func u32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}
func u16(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}
func u64(b []byte, v uint64) []byte {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	return append(b, tmp[:]...)
}

// boxHeader writes the size+type prefix. The size includes the 8-byte
// header itself, so payload length = size - 8.
func boxHeader(b []byte, fourcc string, size uint32) []byte {
	b = u32(b, size)
	b = append(b, fourcc[0], fourcc[1], fourcc[2], fourcc[3])
	return b
}

// makeBox wraps a payload in a box of the given 4cc.
func makeBox(fourcc string, payload []byte) []byte {
	b := make([]byte, 0, 8+len(payload))
	b = boxHeader(b, fourcc, uint32(8+len(payload)))
	b = append(b, payload...)
	return b
}

// makeFullBox wraps a payload in a fullbox. Layout:
//
//	[size:4] [type:4] [version:1] [flags:3] [payload]
//
// total = 8 + 4 + len(payload). Use this for any box whose spec lists
// a version+flags in the header (mvhd, tkhd, mdhd, hdlr, vmhd, dref,
// url, stsd, stts, stsc, stsz, stco, mfhd, tfhd, tfdt, trun).
func makeFullBox(fourcc string, version uint8, flags uint32, payload []byte) []byte {
	total := 8 + 4 + len(payload)
	b := make([]byte, 0, total)
	b = u32(b, uint32(total))
	b = append(b, fourcc[0], fourcc[1], fourcc[2], fourcc[3])
	b = append(b, version)
	b = append(b, byte(flags>>16), byte(flags>>8), byte(flags))
	b = append(b, payload...)
	return b
}
