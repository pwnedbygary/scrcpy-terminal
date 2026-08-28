// spspps.go — extract SPS + PPS NAL units from a scrcpy CONFIG packet
// (Annex-B stream containing SPS and PPS, each preceded by a 4-byte
// start code 00 00 00 01).
package engine

// ParseSPSPPS returns the SPS and PPS NAL units (without start codes)
// found in a CONFIG packet. Returns nil, nil if either is missing.
func ParseSPSPPS(config []byte) (sps, pps []byte) {
	// Walk the Annex-B stream, find the 00 00 00 01 start codes,
	// and pick the SPS (NAL type 7, first byte 0x67) and PPS (NAL type 8,
	// first byte 0x68).
	type nal struct{ start, end int }
	nals := []nal{}
	i := 0
	for i+3 < len(config) {
		if config[i] == 0 && config[i+1] == 0 && config[i+2] == 0 && config[i+3] == 1 {
			nals = append(nals, nal{start: i + 4})
			i += 4
		} else {
			i++
		}
	}
	for idx, n := range nals {
		end := len(config)
		if idx+1 < len(nals) {
			end = nals[idx+1].start - 4 // back off to before next start code
		}
		nal := config[n.start:end]
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1F
		switch nalType {
		case 7: // SPS
			sps = append([]byte(nil), nal...)
		case 8: // PPS
			pps = append([]byte(nil), nal...)
		}
	}
	return
}

// ExtractSPSDimensions returns the coded width/height from an H.264 SPS.
// Falls back to 0,0 on parse errors. The scrcpy server sends a session
// header with the real display dimensions, so this is only used to
// sanity-check the fMP4 init segment if we don't have a session yet.
func ExtractSPSDimensions(sps []byte) (w, h int) {
	if len(sps) < 5 {
		return
	}
	// Exp-Golomb walk; see the H.264 spec for the SPS RBSP layout.
	// We re-implement just enough to reach pic_width_in_mbs_minus1 and
	// pic_height_in_map_units_minus1.
	rbsp := annexBRBSP(sps)
	if len(rbsp) < 4 {
		return
	}
	// rbsp[0] = profile_idc, rbsp[1] = constraint_set, rbsp[2] = level
	// then exp-golomb sequence_id, log2_max_frame_num, pic_order_cnt,
	// max_num_ref_frames, gaps, pic_width_in_mbs_minus1, pic_height_in_map_units_minus1
	br := newBitReader(rbsp[3:])
	br.readUE() // seq_parameter_set_id
	// chroma format etc depend on profile; for high profile skip a few
	if sps[1] == 100 || sps[1] == 110 || sps[1] == 122 ||
		sps[1] == 244 || sps[1] == 44 || sps[1] == 83 ||
		sps[1] == 86 || sps[1] == 118 || sps[1] == 128 {
		br.readUE() // chroma_format_idc
		if br.lastUE == 3 {
			br.readBit() // separate_colour_plane_flag
		}
		br.readUE() // bit_depth_luma_minus8
		br.readUE() // bit_depth_chroma_minus8
		br.readBit() // qpprime_y_zero_transform_bypass_flag
		if br.readBit() != 0 { // seq_scaling_matrix_present_flag
			for i := 0; i < 8; i++ {
				if br.readBit() != 0 {
					br.readUE()
				}
			}
		}
	}
	br.readUE()  // log2_max_frame_num_minus4
	br.readUE()  // pic_order_cnt_type
	if br.lastUE == 0 {
		br.readUE() // log2_max_pic_order_cnt_lsb_minus4
	} else if br.lastUE == 1 {
		br.readBit() // delta_pic_order_always_zero_flag
		br.readSE()  // offset_for_non_ref_pic
		br.readSE()  // offset_for_top_to_bottom_field
		n := br.readUE()
		for i := 0; i < int(n); i++ {
			br.readSE()
		}
	}
	br.readUE() // max_num_ref_frames
	br.readBit() // gaps_in_frame_num_value_allowed_flag
	wMbs := br.readUE() + 1
	hMapUnits := br.readUE() + 1
	w = int(wMbs) * 16
	h = int(hMapUnits) * 16
	return
}

func annexBRBSP(annexB []byte) []byte {
	// Strip emulation prevention bytes (00 00 03 → 00 00)
	out := make([]byte, 0, len(annexB))
	for i := 0; i < len(annexB); i++ {
		if i+2 < len(annexB) && annexB[i] == 0 && annexB[i+1] == 0 && annexB[i+2] == 3 {
			out = append(out, 0, 0)
			i += 2
		} else {
			out = append(out, annexB[i])
		}
	}
	return out
}

type bitReader struct {
	b      []byte
	i      int  // bit position
	lastUE uint32 // last readUE result
}

func newBitReader(b []byte) *bitReader { return &bitReader{b: b} }

func (r *bitReader) readBit() uint32 {
	bi := r.i % 8
	by := r.i / 8
	if by >= len(r.b) {
		return 0
	}
	v := (r.b[by] >> (7 - bi)) & 1
	r.i++
	return uint32(v)
}

// readUE reads one unsigned exp-golomb code. Sets lastUE.
func (r *bitReader) readUE() uint32 {
	zeros := 0
	for r.readBit() == 0 {
		zeros++
		if zeros > 32 {
			r.lastUE = 0
			return 0
		}
	}
	v := (uint32(1) << zeros) - 1 + r.readBits(zeros)
	r.lastUE = v
	return v
}

// readSE reads signed exp-golomb.
func (r *bitReader) readSE() int32 {
	k := int32(r.readUE())
	if k%2 == 0 {
		return -(k / 2)
	}
	return (k + 1) / 2
}

func (r *bitReader) readBits(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v = (v << 1) | r.readBit()
	}
	return v
}
