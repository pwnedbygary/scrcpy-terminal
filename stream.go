package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// frameSink receives decoded frames from the video demux goroutine. The TUI
// sink draws cells, the web sink encodes/hands off to browsers; both must be
// non-blocking, because the demux loop is the pipeline's heartbeat.
type frameSink interface {
	frame(f *videoFrame)
}

// streamState owns the video/audio demux loops.
type streamState struct {
	sess *session
	cfg  config
	ctrl *controller
	sink frameSink

	// current video geometry (set by session packets)
	videoW, videoH int

	// pending config packet (h264 SPS/PPS) to prepend to next media packet
	config []byte

	// canvas / fit geometry (terminal cell grid)
	canvasW, canvasH int // cols x (2*rows): RGBA canvas
	fitW, fitH       int // letterboxed video region inside canvas
	fitOffX, fitOffY int // in canvas pixels

	// webMode makes the canvas the video's own size instead of the terminal
	// cell grid. The browser is the display, so there is no terminal cell grid
	// to honour, and every client must share ONE canvas: the frame buffer is
	// allocated once per frame, so a client that invents its own geometry
	// would hand the JPEG encoder a buffer sized for someone else's canvas
	// (that read past the buffer and crashed scterm). Browsers scale the
	// canvas to their viewport with CSS, which is free and lossless.
	webMode bool

	// Set by the resize/session paths (main loop), consumed per frame by
	// runVideo: replaces the per-frame ioctl poll with one termSize() call
	// per geometry event.
	geometryDirty atomic.Bool

	// fps
	currentFPS float64
	frameCount atomic.Int64

	// canvas pool: hand the renderer its own buffer so the decoder can
	// immediately reuse its scratch canvas without an alloc+copy per frame.
	canvasPool [2][]byte

	// audio stat
	audioBytes int64 // decoded bytes handed to the sink
	// audioPeak is the largest |sample| seen (0 = digital silence). Atomic
	// because the status paths read it from another goroutine than the audio
	// demux loop that writes it.
	audioPeak atomic.Int32
	opusPeak  int16       // peak right after opus decode (before resample)
	packets   int64       // audio media packets received
	pktSizes  map[int]int // histogram: config/first packets

	// A/V lag probe: the device timestamps audio packets and video frames on
	// the same monotonic clock, so ptsLagUs = video.pts - audio.pts at arrival
	// time shows how much the audio stream lags the video pipeline end-to-end
	// (capture-side buffering, e.g. remote submix, shows up here, not in the
	// host playback buffer).
	lastVideoPts atomic.Int64 // us
	lastAudioPts atomic.Int64 // us

	// pcmTaps receive every decoded s16le stereo 48k buffer in addition to the
	// local PulseAudio sink. Web/window clients need the same PCM the sink
	// gets, and a tap must never block the demux loop (see runAudio).
	pcmMu   sync.Mutex
	pcmTaps []func([]byte)
}

// addPCMTap registers a consumer of decoded PCM. Taps are called from the
// audio demux goroutine: they must not block (drop or buffer internally).
func (s *streamState) addPCMTap(fn func([]byte)) {
	if fn == nil {
		return
	}
	s.pcmMu.Lock()
	s.pcmTaps = append(s.pcmTaps, fn)
	s.pcmMu.Unlock()
}

// fanPCM hands a decoded PCM buffer to every registered tap.
func (s *streamState) fanPCM(pcm []byte) {
	s.pcmMu.Lock()
	taps := s.pcmTaps
	s.pcmMu.Unlock()
	for _, fn := range taps {
		fn(pcm)
	}
}

// noteAudioPeak records the largest |sample| seen so far. It is called from the
// audio demux goroutine and read from the status paths, hence the atomic.
func (s *streamState) noteAudioPeak(pk int16) {
	for {
		old := s.audioPeak.Load()
		if int32(pk) <= old {
			return
		}
		if s.audioPeak.CompareAndSwap(old, int32(pk)) {
			return
		}
	}
}

// peakAudio returns the largest |sample| seen since start (0 = the device has
// sent nothing but digital silence).
func (s *streamState) peakAudio() int16 {
	if s == nil {
		return 0
	}
	return int16(s.audioPeak.Load())
}

// audioBytesSeen returns the decoded audio bytes handed to the sink.
func (s *streamState) audioBytesSeen() int64 {
	if s == nil {
		return 0
	}
	return s.audioBytes
}

type videoFrame struct {
	rgb  []byte
	w, h int // video frame dims (session size)
	cw   int // canvas dims (rgb length = cw*ch*4)
	ch   int
	fps  float64
}

func newStreamState(sess *session, cfg config, ctrl *controller) *streamState {
	return &streamState{sess: sess, cfg: cfg, ctrl: ctrl}
}

// setSink installs the frame consumer (before runVideo starts).
func (s *streamState) setSink(f frameSink) { s.sink = f }

// frames returns (decoded frame count, video width, video height).
func (s *streamState) frames() (int64, int, int) {
	return s.frameCount.Load(), s.videoW, s.videoH
}

// updateGeometry recomputes canvas + fit after a terminal resize or a video
// session change. Cheap enough to call per frame (one ioctl); the canvas
// realloc only happens when the size actually changed.
func (s *streamState) updateGeometry() {
	if s.webMode {
		// The canvas is the video, at the video's own resolution: fit is the
		// whole canvas and there is no letterboxing. JPEG 4:2:0 needs even
		// dimensions, and a zero geometry (before the first session header)
		// must not allocate a frame buffer.
		cw, ch := s.videoW&^1, s.videoH&^1
		if cw < 2 || ch < 2 {
			s.canvasW, s.canvasH = 0, 0
			s.fitW, s.fitH = 0, 0
			s.fitOffX, s.fitOffY = 0, 0
			return
		}
		s.canvasW, s.canvasH = cw, ch
		s.fitW, s.fitH = cw, ch
		s.fitOffX, s.fitOffY = 0, 0
		return
	}

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
		if !isConfig {
			s.lastVideoPts.Store(ptsUs)
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
					s.frameCount.Add(1)
					s.deliverFrame(&videoFrame{rgb: cpy, w: s.videoW, h: s.videoH,
						cw: s.videoW, ch: s.videoH, fps: fps()})
				}
				continue
			}
			// Scale directly into a pooled slot (ping-pong): no per-frame
			// scratch copy. Letterbox pixels stay black because slots are
			// zeroed at make() and swscale only writes the fit region.
			//
			// A zero canvas (web mode before the first session header tells us
			// the video size) has no frame buffer to scale into: drop the frame
			// rather than hand swscale an empty destination.
			if s.canvasW < 2 || s.canvasH < 2 || s.fitW < 2 || s.fitH < 2 {
				continue
			}
			buf := s.takePooled(s.canvasW * s.canvasH * 4)
			reg := buf[(s.fitOffY*s.canvasW+s.fitOffX)*4:]
			if _, _, err := vdecScaleStride(dec, reg, s.canvasW*4, s.fitW, s.fitH); err != nil {
				s.returnPooled(buf)
				continue
			}
			s.frameCount.Add(1)
			s.deliverFrame(&videoFrame{rgb: buf, w: s.videoW, h: s.videoH,
				cw: s.canvasW, ch: s.canvasH, fps: fps()})
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
			_, ptsUs, _, _, size := parsePacketHeader(he)
			if size == 0 {
				continue
			}
			s.packets++
			s.lastAudioPts.Store(ptsUs)
			payload := make([]byte, size)
			if _, err := io.ReadFull(r, payload); err != nil {
				return fmt.Errorf("audio raw packet: %w", err)
			}
			s.audioBytes += int64(len(payload))
			s.noteAudioPeak(maxAbsInt16(payload))
			audioOut.writePCM16(payload)
			s.fanPCM(payload)
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
		s.lastAudioPts.Store(ptsUs)
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
				s.noteAudioPeak(maxAbsInt16(pcm[:n]))
			}
			audioOut.writePCM16(pcm[:n])
			s.fanPCM(pcm[:n])
		}
	}
}

// deliverFrame hands a frame to the sink, recycling the buffer if there is no
// sink at all (headless without --web).
func (s *streamState) deliverFrame(f *videoFrame) {
	if s.sink == nil {
		s.returnPooled(f.rgb)
		return
	}
	s.sink.frame(f)
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
