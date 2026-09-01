package main

import (
	"encoding/binary"
	"io"
)

// scrcpy wire constants
const (
	codecH264 = 0x68323634
	codecH265 = 0x68323635
	codecAV1  = 0x00617631
	codecVP8  = 0x00767038
	codecVP9  = 0x00767039
	codecOpus = 0x6f707573
	codecAAC  = 0x00616163
	codecFLAC = 0x666c6163
	codecRAW  = 0x00726177
)

// ---------------------------------------------------------------------------
// packet framing (12-byte header, big-endian)
// ---------------------------------------------------------------------------

func readCodecID(r io.Reader) (int, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(b[:])), nil
}

// parsePacketHeader decodes a 12-byte header for a media packet:
// returns (isSession, ptsUs, isConfig, isKeyframe, size)
const (
	pktFlagConfig   = 1 << 62
	pktFlagKeyframe = 1 << 61
	pktPTSMask      = (1 << 61) - 1
)

// parsePacketHeader decodes a 12-byte header for a media packet:
// returns (isSession, ptsUs, isConfig, isKeyframe, size)
func parsePacketHeader(he []byte) (isSession bool, ptsUs int64, isConfig, isKey bool, size uint32) {
	if he[0]&0x80 != 0 {
		return true, 0, false, false, 0
	}
	pts := binary.BigEndian.Uint64(he[0:8])
	isConfig = pts&pktFlagConfig != 0
	isKey = pts&pktFlagKeyframe != 0
	size = binary.BigEndian.Uint32(he[8:12])
	return false, int64(pts & pktPTSMask), isConfig, isKey, size
}

// sessionHeader returns width, height, clientResized from a session packet.
func sessionHeader(he []byte) (w, h uint32, clientResized bool) {
	w = binary.BigEndian.Uint32(he[4:8])
	h = binary.BigEndian.Uint32(he[8:12])
	clientResized = he[3]&1 != 0
	return
}

// ---------------------------------------------------------------------------
// control messages (client -> device), see scrcpy control_msg.c
// ---------------------------------------------------------------------------

const (
	ctrlInjectKeycode   = 0
	ctrlInjectText      = 1
	ctrlInjectTouch     = 2
	ctrlInjectScroll    = 3
	ctrlBackOrScreenOn  = 4
	ctrlExpandNotif     = 5
	ctrlExpandSettings  = 6
	ctrlCollapsePanels  = 7
	ctrlGetClipboard    = 8
	ctrlSetClipboard    = 9
	ctrlSetDisplayPower = 10
	ctrlRotateDevice    = 11
	ctrlStartApp        = 16
	ctrlResetVideo      = 17
	ctrlResizeDisplay   = 21
)

// Android event constants
const (
	keyActionDown = 0
	keyActionUp   = 1

	motionActionDown       = 0
	motionActionUp         = 1
	motionActionMove       = 2
	motionActionScroll     = 8
	motionActionButtonDown = 11
	motionActionButtonUp   = 12

	pointerIDMouse  = uint64(0xFFFFFFFFFFFFFFFF) // SC_POINTER_ID_MOUSE = -1
	pointerIDFinger = uint64(0xFFFFFFFFFFFFFFFE) // SC_POINTER_ID_GENERIC_FINGER = -2

	metaShiftOn = 0x01
	metaAltOn   = 0x02
	metaCtrlOn  = 0x1000
)

type position struct {
	x, y    int32
	screenW uint16
	screenH uint16
}

func (p position) marshal(dst []byte) {
	binary.BigEndian.PutUint32(dst[0:4], uint32(p.x))
	binary.BigEndian.PutUint32(dst[4:8], uint32(p.y))
	binary.BigEndian.PutUint16(dst[8:10], p.screenW)
	binary.BigEndian.PutUint16(dst[10:12], p.screenH)
}

// serializeControlMsg returns the wire bytes for a control message.
// The byte layouts mirror sc_control_msg_serialize() exactly.
func serializeControlMsg(msg []byte, mtype byte) []byte {
	out := make([]byte, 256)
	out[0] = mtype
	switch mtype {
	case ctrlInjectKeycode:
		out = out[:14]
		out[1] = msg[0]                                                            // action
		binary.BigEndian.PutUint32(out[2:6], binary.BigEndian.Uint32(msg[1:5]))    // keycode
		binary.BigEndian.PutUint32(out[6:10], binary.BigEndian.Uint32(msg[5:9]))   // repeat
		binary.BigEndian.PutUint32(out[10:14], binary.BigEndian.Uint32(msg[9:13])) // metastate
	case ctrlInjectText:
		// payload = [pad(1)][u32 len][utf8] -> wire = [type][u32 len][utf8]
		text := msg[5:]
		out = out[:1+4+len(text)]
		binary.BigEndian.PutUint32(out[1:5], uint32(len(text)))
		copy(out[5:], text)
	case ctrlInjectTouch:
		out = out[:32]
		out[1] = msg[0]                                                          // action
		binary.BigEndian.PutUint64(out[2:10], binary.BigEndian.Uint64(msg[1:9])) // pointer_id
		copy(out[10:22], msg[9:21])                                              // position (x,y,w,h already BE)
		copy(out[22:24], msg[21:23])                                             // pressure u16fp
		copy(out[24:28], msg[23:27])                                             // action_button
		copy(out[28:32], msg[27:31])                                             // buttons
	case ctrlInjectScroll:
		out = out[:21]
		copy(out[1:13], msg[0:12])   // position
		copy(out[13:15], msg[12:14]) // hscroll i16fp
		copy(out[15:17], msg[14:16]) // vscroll i16fp
		copy(out[17:21], msg[16:20]) // buttons
	case ctrlBackOrScreenOn:
		out = out[:2]
		out[1] = msg[0]
	case ctrlSetClipboard:
		// msg = [seq8][paste1][pad2] text
		out = out[:1+8+1+4]
		binary.BigEndian.PutUint64(out[1:9], binary.BigEndian.Uint64(msg[0:8]))
		out[9] = msg[8]
		text := msg[11:]
		binary.BigEndian.PutUint32(out[10:14], uint32(len(text)))
		out = append(out, text...)
	case ctrlSetDisplayPower:
		out = out[:2]
		out[1] = msg[0]
	case ctrlExpandNotif, ctrlExpandSettings, ctrlCollapsePanels,
		ctrlRotateDevice, ctrlResetVideo:
		out = out[:1]
	case ctrlResizeDisplay:
		out = out[:5]
		binary.BigEndian.PutUint16(out[1:3], binary.BigEndian.Uint16(msg[0:2]))
		binary.BigEndian.PutUint16(out[3:5], binary.BigEndian.Uint16(msg[2:4]))
	default:
		panic("unsupported control message type")
	}
	return out
}

// helpers offered at a higher level:

func keycodeMsg(action, keycode, repeat, metastate uint32) []byte {
	m := make([]byte, 13)
	m[0] = byte(action)
	binary.BigEndian.PutUint32(m[1:5], keycode)
	binary.BigEndian.PutUint32(m[5:9], repeat)
	binary.BigEndian.PutUint32(m[9:13], metastate)
	return m
}

func touchMsg(action byte, pointerID uint64, pos position, pressure uint16, actionButton, buttons uint32) []byte {
	m := make([]byte, 31)
	m[0] = action
	binary.BigEndian.PutUint64(m[1:9], pointerID)
	pos.marshal(m[9:21])
	binary.BigEndian.PutUint16(m[21:23], pressure)
	binary.BigEndian.PutUint32(m[23:27], actionButton)
	binary.BigEndian.PutUint32(m[27:31], buttons)
	return m
}

func scrollMsg(pos position, hscroll, vscroll float32, buttons uint32) []byte {
	m := make([]byte, 20)
	pos.marshal(m[0:12])
	hs := int16(clampF32(hscroll/16, -1, 1) * 32768)
	vs := int16(clampF32(vscroll/16, -1, 1) * 32768)
	binary.BigEndian.PutUint16(m[12:14], uint16(hs))
	binary.BigEndian.PutUint16(m[14:16], uint16(vs))
	binary.BigEndian.PutUint32(m[16:20], buttons)
	return m
}

func clampF32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func floatToU16fp(f float32) uint16 {
	u := uint32(f * 65536)
	if u >= 0xffff {
		u = 0xffff
	}
	return uint16(u)
}
