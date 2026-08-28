// fmp4.go — minimal fragmented-MP4 (fMP4) muxer for live H.264 streams.
//
// One init segment (sent once per MediaSource), then one fragment per
// encoded frame. The whole point is to hand the browser a hardware-
// decodable H.264 stream without a server-side ffmpeg round-trip.
//
// Reference: ISO/IEC 14496-12 (ISO-BMFF) and 14496-15 (AVC file format).
package engine

import (
	"encoding/binary"
	"io"
)

// FMP4Muxer writes init + per-frame fragments for one H.264 video track.
// Not safe for concurrent use; the caller owns the goroutine.
type FMP4Muxer struct {
	seq       uint32 // moof sequence number
	prevPTSuS uint64 // previous PTS in microseconds
	ts        uint32 // track timescale (default 90000 = 90 kHz)
	sps       []byte // H.264 SPS (without Annex-B start code)
	pps       []byte // H.264 PPS (without Annex-B start code)
	w, h      int    // coded dimensions
	wrote     bool   // true after first WriteFrame
}

// NewFMP4Muxer returns a muxer. The track timescale is 90 kHz so PTS in
// microseconds maps via (pts * 90 / 1_000_000) == track-time units.
func NewFMP4Muxer() *FMP4Muxer { return &FMP4Muxer{ts: 90000} }

// SetSPSPPS registers the codec configuration extracted from the engine's
// CONFIG video packet. Must be called before WriteInit.
func (m *FMP4Muxer) SetSPSPPS(sps, pps []byte, width, height int) {
	m.sps, m.pps, m.w, m.h = sps, pps, width, height
}

// WriteInit emits ftyp + moov to w.
func (m *FMP4Muxer) WriteInit(w io.Writer) error {
	if _, err := w.Write(m.buildFtyp()); err != nil {
		return err
	}
	if _, err := w.Write(m.buildMoov()); err != nil {
		return err
	}
	return nil
}

// WriteFrame writes one H.264 access unit (Annex-B) as a fresh moof+mdat
// fragment. isKeyFrame sets the sync-sample flag in trun.
func (m *FMP4Muxer) WriteFrame(w io.Writer, isKeyFrame bool, ptsMicroSec uint64, annexB []byte) error {
	sample := annexBToLengthPrefixed(annexB)
	// baseMediaDecodeTime (tfdt) is the decode time of THIS sample.
	// The track timescale is 90 kHz; PTS is microseconds.
	baseDT := ptsMicroSec * uint64(m.ts) / 1_000_000
	m.prevPTSuS = ptsMicroSec

	moof := m.buildMoof(sample, isKeyFrame, baseDT)
	if _, err := w.Write(moof); err != nil {
		return err
	}
	mdat := make([]byte, 0, 8+len(sample))
	mdat = boxHeader(mdat, "mdat", uint32(8+len(sample)))
	mdat = append(mdat, sample...)
	if _, err := w.Write(mdat); err != nil {
		return err
	}
	m.seq++
	m.wrote = true
	return nil
}

// annexBToLengthPrefixed converts an H.264 Annex-B stream (with 00 00 00 01
// or 00 00 01 start codes) to ISO-BMFF length-prefixed NAL units (4 bytes
// big-endian length, then the NAL payload, no start code).
func annexBToLengthPrefixed(annexB []byte) []byte {
	out := make([]byte, 0, len(annexB))
	i := 0
	for i < len(annexB) {
		// find next start code
		nalStart := -1
		for j := i; j+2 < len(annexB); j++ {
			if annexB[j] == 0 && annexB[j+1] == 0 {
				if annexB[j+2] == 1 {
					nalStart = j + 3
					break
				}
				if j+3 < len(annexB) && annexB[j+2] == 0 && annexB[j+3] == 1 {
					nalStart = j + 4
					break
				}
			}
		}
		if nalStart < 0 {
			out = append(out, annexB[i:]...)
			break
		}
		nalEnd := len(annexB)
		for j := nalStart; j+2 < len(annexB); j++ {
			if annexB[j] == 0 && annexB[j+1] == 0 &&
				(annexB[j+2] == 1 || (j+3 < len(annexB) && annexB[j+2] == 0 && annexB[j+3] == 1)) {
				nalEnd = j
				break
			}
		}
		nal := annexB[nalStart:nalEnd]
		if len(nal) > 0 {
			out = u32(out, uint32(len(nal)))
			out = append(out, nal...)
		}
		i = nalEnd
		if nalEnd == len(annexB) {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// init segment
// ---------------------------------------------------------------------------

func (m *FMP4Muxer) buildFtyp() []byte {
	// ftyp: 8 + 4 (major) + 4 (minor) + 4*N (compat brands)
	brands := []byte("isomiso2mp41avc1")
	body := make([]byte, 0, 8+4+4+len(brands))
	body = append(body, "isom"...) // major brand
	body = u32(body, 512)         // minor version
	body = append(body, brands...)
	return makeBox("ftyp", body)
}

func (m *FMP4Muxer) buildMoov() []byte {
	// moov contains mvhd + trak + (optionally) mvex/trex. trex gives
	// default sample descriptions for fragments; some demuxers (ffprobe,
	// Chromium's MSE) require it even when fragments carry full per-sample
	// metadata.
	return makeBox("moov", append(append(m.buildMvhd(), m.buildTrak()...), m.buildMvex()...))
}

// buildMvex: MovieExtendsBox containing one TrackExtendsBox per trak.
func (m *FMP4Muxer) buildMvex() []byte {
	return makeBox("mvex", m.buildTrex())
}

// buildTrex: TrackExtendsBox (FullBox, version 0, 20 bytes payload).
// Defaults here are largely informational — fragments carry per-sample
// values in trun. The track_id MUST match the corresponding tkhd.
func (m *FMP4Muxer) buildTrex() []byte {
	p := make([]byte, 0, 20)
	p = u32(p, 1) // track_id
	p = u32(p, 1) // default_sample_description_index
	p = u32(p, 0) // default_sample_duration (use 0 = per-fragment)
	p = u32(p, 0) // default_sample_size
	p = u32(p, 0) // default_sample_flags
	return makeFullBox("trex", 0, 0, p)
}

// buildMvhd: MovieHeaderBox (FullBox, version 0, 96 bytes payload after
// the 8+4 fullbox header).
func (m *FMP4Muxer) buildMvhd() []byte {
	p := make([]byte, 0, 96)
	p = u32(p, 0)          // creation_time
	p = u32(p, 0)          // modification_time
	p = u32(p, m.ts)       // timescale (90 kHz)
	p = u32(p, m.ts)       // duration (0 ok for streaming, browser will play to live edge)
	p = u32(p, 0x00010000) // rate 1.0
	p = u16(p, 0x0100)     // volume 1.0
	p = u16(p, 0)          // reserved
	p = u32(p, 0)          // reserved
	p = u32(p, 0)          // reserved
	// 3x3 unity matrix (36 bytes)
	for i := 0; i < 9; i++ {
		if i == 0 || i == 4 || i == 8 {
			p = u32(p, 0x00010000)
		} else {
			p = u32(p, 0)
		}
	}
	// pre-defined (24 bytes)
	p = append(p, make([]byte, 24)...)
	p = u32(p, 2) // next_track_id
	return makeFullBox("mvhd", 0, 0, p)
}

// buildTrak wraps tkhd + mdia in a TrackBox.
func (m *FMP4Muxer) buildTrak() []byte {
	return makeBox("trak", append(m.buildTkhd(), m.buildMdia()...))
}

// buildTkhd: TrackHeaderBox (FullBox, version 0, 80 bytes payload).
// Flags: 0x7 = enabled + in_movie + in_preview.
func (m *FMP4Muxer) buildTkhd() []byte {
	p := make([]byte, 0, 80)
	p = u32(p, 0)    // creation_time
	p = u32(p, 0)    // modification_time
	p = u32(p, 1)    // track_id
	p = u32(p, 0)    // reserved
	p = u32(p, m.ts) // duration
	p = u32(p, 0)    // reserved
	p = u32(p, 0)    // reserved
	p = u16(p, 0)    // layer
	p = u16(p, 0)    // alternate_group
	p = u16(p, 0)    // volume
	p = u16(p, 0)    // reserved
	// 3x3 unity matrix
	for i := 0; i < 9; i++ {
		if i == 0 || i == 4 || i == 8 {
			p = u32(p, 0x00010000)
		} else {
			p = u32(p, 0)
		}
	}
	p = u32(p, uint32(m.w)<<16) // width  (16.16)
	p = u32(p, uint32(m.h)<<16) // height (16.16)
	return makeFullBox("tkhd", 0, 0x7, p)
}

func (m *FMP4Muxer) buildMdia() []byte {
	return makeBox("mdia", append(append(m.buildMdhd(), m.buildHdlr()...), m.buildMinf()...))
}

// buildMdhd: MediaHeaderBox (FullBox, version 0, 24 bytes payload).
func (m *FMP4Muxer) buildMdhd() []byte {
	p := make([]byte, 0, 24)
	p = u32(p, 0)    // creation_time
	p = u32(p, 0)    // modification_time
	p = u32(p, m.ts) // timescale
	p = u32(p, m.ts) // duration
	p = u16(p, 0x55C4) // language 'und'
	p = u16(p, 0)      // pre_defined
	return makeFullBox("mdhd", 0, 0, p)
}

// buildHdlr: HandlerReferenceBox (FullBox, version 0).
func (m *FMP4Muxer) buildHdlr() []byte {
	p := make([]byte, 0, 4+4+12+7)
	p = u32(p, 0)            // pre_defined
	p = append(p, "vide"...) // handler_type
	p = append(p, make([]byte, 12)...)
	p = append(p, "scterm\x00"...)
	return makeFullBox("hdlr", 0, 0, p)
}

func (m *FMP4Muxer) buildMinf() []byte {
	return makeBox("minf", append(append(m.buildVmhd(), m.buildDinf()...), m.buildStbl()...))
}

// buildVmhd: VideoMediaHeaderBox (FullBox, version 0, flags=1).
func (m *FMP4Muxer) buildVmhd() []byte {
	p := make([]byte, 0, 8)
	p = u16(p, 0) // graphicsmode
	p = u16(p, 0) // opcolor × 3
	p = u16(p, 0)
	p = u16(p, 0)
	return makeFullBox("vmhd", 0, 1, p)
}

// buildDinf: DataInformationBox → DataReferenceBox.
func (m *FMP4Muxer) buildDinf() []byte {
	url := makeFullBox("url ", 0, 1, nil)             // flags=1 self-contained, no payload
	drefBody := append(u32(nil, 1), url...)           // entry_count + url box
	dref := makeFullBox("dref", 0, 0, drefBody)
	return makeBox("dinf", dref)
}

func (m *FMP4Muxer) buildStbl() []byte {
	stsd := makeFullBox("stsd", 0, 0, append(u32(nil, 1), m.buildAvc1()...))
	stts := makeFullBox("stts", 0, 0, u32(nil, 0))
	stsc := makeFullBox("stsc", 0, 0, u32(nil, 0))
	stsz := makeFullBox("stsz", 0, 0, append(u32(nil, 0), u32(nil, 0)...))
	stco := makeFullBox("stco", 0, 0, u32(nil, 0))
	total := len(stsd) + len(stts) + len(stsc) + len(stsz) + len(stco)
	out := make([]byte, 0, 8+total)
	out = boxHeader(out, "stbl", uint32(8+total))
	out = append(out, stsd...)
	out = append(out, stts...)
	out = append(out, stsc...)
	out = append(out, stsz...)
	out = append(out, stco...)
	return out
}

// buildAvc1 wraps an avcC configuration in an AVC1 sample entry.
func (m *FMP4Muxer) buildAvc1() []byte {
	avcC := m.buildAvcC()
	// VisualSampleEntry layout (ISO/IEC 14496-12 §8.5.2.2):
	// 6 reserved + 2 data_ref_index + 16 pre-defined/reserved + 2 width +
	// 2 height + 4 horizres + 4 vertres + 4 reserved + 2 frame_count +
	// 32 compressorname + 2 depth + 2 pre_defined = 78 bytes
	body := make([]byte, 0, 78+len(avcC))
	body = append(body, make([]byte, 6)...)        // reserved
	body = u16(body, 1)                             // data_reference_index
	body = append(body, make([]byte, 16)...)        // pre-defined + reserved
	body = u16(body, uint16(m.w))                   // width
	body = u16(body, uint16(m.h))                   // height
	body = u32(body, 0x00480000)                    // horizresolution 72 dpi
	body = u32(body, 0x00480000)                    // vertresolution 72 dpi
	body = u32(body, 0)                             // reserved
	body = u16(body, 1)                             // frame_count
	body = append(body, make([]byte, 32)...)        // compressorname
	body = u16(body, 0x0018)                        // depth
	body = u16(body, 0xffff)                        // pre_defined
	body = append(body, avcC...)
	return makeBox("avc1", body)
}

// buildAvcC writes the AVCDecoderConfigurationRecord as the avcC box body.
// Reads SPS[1..3] for the profile/level bytes; attaches SPS+PPS verbatim.
func (m *FMP4Muxer) buildAvcC() []byte {
	if len(m.sps) < 4 {
		return makeBox("avcC", nil)
	}
	body := make([]byte, 0, 11+len(m.sps)+len(m.pps))
	body = append(body, 1)        // configurationVersion
	body = append(body, m.sps[1]) // AVCProfileIndication
	body = append(body, m.sps[2]) // profile_compatibility
	body = append(body, m.sps[3]) // AVCLevelIndication
	body = append(body, 0xFF)     // lengthSizeMinusOne=3 (4 bytes) + reserved
	body = append(body, 0xE1)     // numOfSequenceParameterSets=1
	body = u16(body, uint16(len(m.sps)))
	body = append(body, m.sps...)
	body = append(body, 1) // numOfPictureParameterSets=1
	body = u16(body, uint16(len(m.pps)))
	body = append(body, m.pps...)
	return makeBox("avcC", body)
}

// ---------------------------------------------------------------------------
// per-frame fragment
// ---------------------------------------------------------------------------

// buildMoof writes moof[mfhd, traf[tfhd, tfdt, trun]] for one sample.
// trun's data_offset is filled in once the moof size is known.
func (m *FMP4Muxer) buildMoof(sample []byte, isKeyFrame bool, baseDT uint64) []byte {
	// sample flags for trun — depends on isKeyFrame
	//   isKeyFrame: sample_depends_on=1 (I), sample_is_depended_on=0
	//   not key:    sample_depends_on=2 (P), sample_is_depended_on=1
	// ISO/IEC 14496-12 §8.8.3.1: layout is
	//   reserved(4) is_leading(2) depends_on(2) is_depended_on(2) has_redundancy(2)
	//   sample_degradation_priority(2) sample_is_non_sync_sample(1) sample_padding_value(1)
	// We want: depends_on=1 for IDR, depends_on=2 for P, and set the
	// is_non_sync_sample bit (bit 16 of the u32) for non-IDR.
	var sampleFlags uint32
	if isKeyFrame {
		// depends_on=1 (I) at bits 4-5: 01 << 4 = 0x10
		sampleFlags = uint32(0x01) << 4
	} else {
		// depends_on=2 (P) at bits 4-5: 10 << 4 = 0x20
		// is_non_sync_sample (bit 16): 1 << 16
		sampleFlags = (uint32(0x02) << 4) | (uint32(0x01) << 16)
	}

	// tfhd flags: 0x020000 default-base-is-moof + 0x000008 default-sample-duration-present
	//            + 0x000010 default-sample-size-present + 0x000020 default-sample-flags-present
	const tfhdFlags = 0x020008 | 0x000010 | 0x000020
	tfhdBody := make([]byte, 0, 16)
	tfhdBody = u32(tfhdBody, 1)                // track_id
	tfhdBody = u32(tfhdBody, uint32(0))        // default_sample_duration (0 = run overrides)
	tfhdBody = u32(tfhdBody, uint32(len(sample))) // default_sample_size
	tfhdBody = u32(tfhdBody, sampleFlags)      // default_sample_flags
	tfhd := makeFullBox("tfhd", 0, tfhdFlags, tfhdBody)

	// tfdt v1: 8-byte baseMediaDecodeTime
	tfdt := makeFullBox("tfdt", 1, 0, u64(nil, baseDT))

	// trun: 1 sample, flags = data-offset + size + flags + cts
	//   0x000001 data-offset-present
	//   0x000004 sample-size-present
	//   0x000400 sample-flags-present
	//   0x000800 sample-cts-time-offset-present
	const trunFlags = 0x000001 | 0x000004 | 0x000400 | 0x000800
	trunBody := make([]byte, 0, 24)
	trunBody = u32(trunBody, 1)                  // sample_count
	trunBody = u32(trunBody, 0)                  // data_offset (patched below)
	trunBody = u32(trunBody, uint32(len(sample))) // sample_size
	trunBody = u32(trunBody, sampleFlags)        // sample_flags
	trunBody = u32(trunBody, 0)                  // sample_composition_time_offset (0 = no offset)
	trun := makeFullBox("trun", 0, trunFlags, trunBody)

	// mfhd: 4-byte sequence_number
	mfhd := makeFullBox("mfhd", 0, 0, u32(nil, m.seq))

	// traf
	traf := makeBox("traf", append(append(tfhd, tfdt...), trun...))
	// moof
	moof := makeBox("moof", append(mfhd, traf...))

	// Patch trun's data_offset: it's the offset from the start of moof
	// to the first byte of sample data in mdat. With default-base-is-moof
	// (tfhd flag 0x020000) the base IS the start of moof, so the offset
	// is exactly len(moof).
	//
	// Layout (offsets within moof):
	//   [0..8)   moof header
	//   [8..24)  mfhd (16 bytes)
	//   [24..32) traf header (8 bytes)
	//   [32..60) tfhd (28 bytes)
	//   [60..80) tfdt (20 bytes)
	//   [80..)   trun — data_offset at offset 16 within trun
	const trunDataOffsetInMoof = 80 + 16
	binary.BigEndian.PutUint32(moof[trunDataOffsetInMoof:], uint32(len(moof)))
	return moof
}
