package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"
)

// streamState owns the video/audio demux loops.
type streamState struct {
	sess    *session
	cfg     config
	ctrl    *controller
	deliver chan *videoFrame

	// current video geometry (set by session packets)
	videoW, videoH int

	// pending config packet (h264 SPS/PPS) to prepend to next media packet
	config []byte

	// canvas / fit geometry (terminal cell grid)
	canvasW, canvasH int // cols x (2*rows): RGBA canvas
	fitW, fitH       int // letterboxed video region inside canvas
	fitOffX, fitOffY int // in canvas pixels

	// Set by the resize/session paths (main loop), consumed per frame by
	// runVideo: replaces the per-frame ioctl poll with one termSize() call
	// per geometry event.
	geometryDirty atomic.Bool

	// fps
	currentFPS float64

	// canvas pool: hand the renderer its own buffer so the decoder can
	// immediately reuse its scratch canvas without an alloc+copy per frame.
	canvasPool [2][]byte

	// audio stat
	audioBytes int64       // decoded bytes handed to the sink
	audioPeak  int16       // max |sample| seen (0 = silence, real audio > 0)
	opusPeak   int16       // peak right after opus decode (before resample)
	packets    int64       // audio media packets received
	pktSizes   map[int]int // histogram: config/first packets
}

type videoFrame struct {
	rgb  []byte
	w, h int // video frame dims (session size)
	cw   int // canvas dims (rgb length = cw*ch*4)
	ch   int
	fps  float64
}

func newStreamState(sess *session, cfg config, ctrl *controller) *streamState {
	return &streamState{
		sess:    sess,
		cfg:     cfg,
		ctrl:    ctrl,
		deliver: make(chan *videoFrame, 1), // drop-old semantics
	}
}

// updateGeometry recomputes canvas + fit after a terminal resize or a video
// session change. Cheap enough to call per frame (one ioctl); the canvas
// realloc only happens when the size actually changed.
func (s *streamState) updateGeometry() {
	cols, rows := termSize()
	rows-- // status bar
	if cols < 2 {
		cols = 2
	}
	if rows < 2 {
		rows = 2
	}
	s.canvasW = cols
	s.canvasH = 2 * rows

	fw, fh := s.videoW, s.videoH
	if fw <= 0 || fh <= 0 {
		// nothing yet: fit is the full canvas
		s.fitW, s.fitH = s.canvasW, s.canvasH
		s.fitOffX, s.fitOffY = 0, 0
		return
	}
	scaleX := float64(s.canvasW) / float64(fw)
	scaleY := float64(s.canvasH) / float64(fh)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}
	if scale > 1 {
		scale = 1 // never upscale past source (crisper + faster)
	}
	s.fitW = int(float64(fw) * scale)
	s.fitH = int(float64(fh) * scale)
	if s.fitW%2 == 1 {
		s.fitW--
	}
	if s.fitH%2 == 1 {
		s.fitH--
	}
	if s.fitW < 2 {
		s.fitW = 2
	}
	if s.fitH < 2 {
		s.fitH = 2
	}
	s.fitOffX = (s.canvasW - s.fitW) / 2
	s.fitOffY = (s.canvasH - s.fitH) / 2
}

// canvasSize returns the RGBA canvas dimensions.
func (s *streamState) canvasSize() (w, h int) { return s.canvasW, s.canvasH }

// markGeometryDirty schedules a geometry recompute on the next video frame:
// one termSize() ioctl per resize/session event instead of one per decoded
// frame. Called from the main loop (evResize), so it is atomic.
func (s *streamState) markGeometryDirty() { s.geometryDirty.Store(true) }

// resetPool discards pooled canvases after a geometry change: fresh slots are
// zeroed at make(), which keeps letterbox pixels black and avoids stale canvas
// sizes lingering in the pool.
func (s *streamState) resetPool() {
	s.canvasPool[0] = nil
	s.canvasPool[1] = nil
}

// mapCell maps a terminal cell (1-based) to a video-frame position, using the
// letterbox geometry. The position's screen_size MUST be the video frame size
// (the server drops events with mismatched sizes).
func (s *streamState) mapCell(cellX, cellY int) position {
	if s.videoW <= 0 || s.videoH <= 0 {
		return position{screenW: uint16(maxInt(s.videoW, 0)), screenH: uint16(maxInt(s.videoH, 0))}
	}
	// cell center in canvas px (canvas y-space uses 2 px per cell row)
	px := float64(cellX-1)*2 + 1 // center in half-pixel units
	py := float64(cellY-1)*2 + 1
	// canvas pixel space: col x maps to px/2... careful: x cells span 1 px each
	px = float64(cellX-1) + 0.5
	py = (float64(cellY-1) + 0.5) * 2 // canvas pixel y (2 per cell row)

	x := int32((px - float64(s.fitOffX)) / float64(s.fitW) * float64(s.videoW))
	y := int32((py - float64(s.fitOffY)) / float64(s.fitH) * float64(s.videoH))
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if int(x) >= s.videoW {
		x = int32(s.videoW - 1)
	}
	if int(y) >= s.videoH {
		y = int32(s.videoH - 1)
	}
	return position{x: x, y: y, screenW: uint16(s.videoW), screenH: uint16(s.videoH)}
}

// runVideo demuxes the video socket: headers -> packets -> decoder.
func (s *streamState) runVideo() error {
	if s.sess.video == nil {
		return nil
	}
	defer s.sess.video.Close()

	codecID, err := readCodecID(s.sess.video)
	if err != nil {
		return fmt.Errorf("video codec id: %w", err)
	}
	if codecID == 0 || codecID == 1 {
		return fmt.Errorf("video stream disabled by device (code=%d)", codecID)
	}
	if codecID != codecH264 {
		return fmt.Errorf("unsupported video codec 0x%08x", codecID)
	}
	r := bufio.NewReaderSize(s.sess.video, 1<<20)

	// First packet must be a session header.
	he := make([]byte, 12)
	if _, err := io.ReadFull(r, he); err != nil {
		return fmt.Errorf("session header: %w", err)
	}
	isSession, _, _, _, _ := parsePacketHeader(he)
	if !isSession {
		return fmt.Errorf("expected session header first")
	}
	w, h, _ := sessionHeader(he)
	s.videoW, s.videoH = int(w), int(h)
	s.updateGeometry() // initial geometry; pool is empty so no reset needed

	dec := vdecOpen(codecH264, s.videoW, s.videoH)
	if dec == nil {
		return fmt.Errorf("could not open h264 decoder")
	}
	defer vdecFree(dec)

	fps := s.fpsCounter()

	for {
		if _, err := io.ReadFull(r, he); err != nil {
			return fmt.Errorf("stream ended: %w", err)
		}
		isSession, ptsUs, isConfig, _, size := parsePacketHeader(he)
		if isSession {
			neww, newh, _ := sessionHeader(he)
			if int(neww) != s.videoW || int(newh) != s.videoH {
				s.videoW, s.videoH = int(neww), int(newh)
				s.markGeometryDirty()
			}
			continue
		}
		if size == 0 {
			continue
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("packet: %w", err)
		}

		if isConfig {
			s.config = append(s.config[:0], payload...)
			continue
		}

		packet := payload
		if len(s.config) > 0 {
			packet = append(append([]byte{}, s.config...), payload...)
			s.config = s.config[:0]
		}

		if err := vdecSend(dec, packet, ptsUs); err != nil {
			continue
		}
		for {
			hasFrame, err := vdecRecv(dec)
			if err != nil {
				return err
			}
			if !hasFrame {
				break
			}
			// Terminal may have been resized since the last frame (SIGWINCH):
			// consume the flag set by evResize/session paths, so termSize()
			// runs once per geometry event instead of every decoded frame.
			if s.geometryDirty.Swap(false) {
				s.updateGeometry()
				s.resetPool()
			}
			// dump mode: render the frame at FULL resolution for verification
			if s.cfg.dumpFrames != "" {
				full := make([]byte, s.videoW*s.videoH*4)
				if _, _, err := vdecScaleStride(dec, full, s.videoW*4, s.videoW, s.videoH); err == nil {
					cpy := s.takePooled(len(full))
					copy(cpy, full)
					s.deliver <- &videoFrame{rgb: cpy, w: s.videoW, h: s.videoH,
						cw: s.videoW, ch: s.videoH, fps: fps()}
				}
				continue
			}
			// Scale directly into a pooled slot (ping-pong): no per-frame
			// scratch copy. Letterbox pixels stay black because slots are
			// zeroed at make() and swscale only writes the fit region.
			buf := s.takePooled(s.canvasW * s.canvasH * 4)
			reg := buf[(s.fitOffY*s.canvasW+s.fitOffX)*4:]
			if _, _, err := vdecScaleStride(dec, reg, s.canvasW*4, s.fitW, s.fitH); err != nil {
				s.returnPooled(buf)
				continue
			}
			frame := &videoFrame{rgb: buf, w: s.videoW, h: s.videoH,
				cw: s.canvasW, ch: s.canvasH, fps: fps()}
			select {
			case s.deliver <- frame:
			default:
				// drop-old: replace a queued frame with the freshest. Receive
				// must be non-blocking: the renderer may have already taken
				// the old frame (it returns its slot after drawing).
				select {
				case old := <-s.deliver:
					s.returnPooled(old.rgb)
				default:
				}
				s.deliver <- frame
			}
		}
	}
}

// fpsCounter returns a closure sampled every 500ms.
func (s *streamState) fpsCounter() func() float64 {
	t0 := timeNowUnixNano()
	lastT := t0
	frames := 0
	return func() float64 {
		frames++
		now := timeNowUnixNano()
		if now-lastT >= 500e6 {
			s.currentFPS = float64(frames) * 1e9 / float64(now-t0)
			lastT = now
			t0 = now
			frames = 0
		}
		return s.currentFPS
	}
}

// runAudio demuxes the audio socket.
func (s *streamState) runAudio(audioOut *audioSink) error {
	if s.sess.audio == nil {
		return nil
	}
	defer s.sess.audio.Close()

	codecID, err := readCodecID(s.sess.audio)
	if err != nil {
		return fmt.Errorf("audio codec id: %w", err)
	}
	if codecID == 0 {
		return nil // disabled
	}
	if codecID == 1 {
		return fmt.Errorf("audio config error")
	}

	r := bufio.NewReaderSize(s.sess.audio, 1<<20)
	he := make([]byte, 12)
	pcm := make([]byte, 1<<16)

	if adecIsRaw(codecID) {
		// raw s16le stereo 48k: packets are the samples themselves.
		for {
			if _, err := io.ReadFull(r, he); err != nil {
				return fmt.Errorf("audio raw: %w", err)
			}
			_, _, _, _, size := parsePacketHeader(he)
			if size == 0 {
				continue
			}
			payload := make([]byte, size)
			if _, err := io.ReadFull(r, payload); err != nil {
				return fmt.Errorf("audio raw packet: %w", err)
			}
			audioOut.writePCM16(payload)
		}
	}

	dec := adecOpen(codecID)
	if dec == nil {
		return fmt.Errorf("unsupported audio codec 0x%08x", codecID)
	}
	defer adecFree(dec)

	for {
		if _, err := io.ReadFull(r, he); err != nil {
			return fmt.Errorf("audio stream ended: %w", err)
		}
		isSession, ptsUs, isConfig, _, size := parsePacketHeader(he)
		if isSession {
			continue
		}
		if size == 0 {
			continue
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("audio packet: %w", err)
		}
		if isConfig {
			// opus: the config packet contains OpusHead; libavcodec does NOT
			// need it as extradata (we hardcode 48k stereo), but keep the
			// stats honest.
			if s.pktSizes != nil {
				s.pktSizes[len(payload)]++
			}
			continue
		}
		s.packets++
		if s.cfg.audioDump != "" {
			// raw wire capture: 4-byte size + payload, for offline analysis
			f := audioDumpFile(s.cfg.audioDump)
			if f != nil {
				var sz [4]byte
				binary.BigEndian.PutUint32(sz[:], uint32(len(payload)))
				f.Write(sz[:])
				f.Write(payload)
			}
		}
		if err := adecSend(dec, payload, ptsUs); err != nil {
			continue
		}
		for {
			n, err := adecRecv(dec, pcm)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			s.audioBytes += int64(n)
			if s.cfg.audio {
				if pk := maxAbsInt16(pcm[:n]); pk > s.audioPeak {
					s.audioPeak = pk
				}
			}
			audioOut.writePCM16(pcm[:n])
		}
	}
}

// takePooled returns a buffer of at least n bytes, reusing pooled canvases
// when available (frames freed by the renderer via returnPooled).
func (s *streamState) takePooled(n int) []byte {
	for i, b := range s.canvasPool {
		if b != nil && cap(b) >= n {
			s.canvasPool[i] = nil
			return b[:n]
		}
	}
	return make([]byte, n)
}

// returnPooled gives a buffer back for reuse.
func (s *streamState) returnPooled(b []byte) {
	if b == nil {
		return
	}
	for i := range s.canvasPool {
		if s.canvasPool[i] == nil {
			s.canvasPool[i] = b
			return
		}
	}
}
