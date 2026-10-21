package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
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

	// audioOff is set once the device has turned the audio stream off (see
	// errAudioOff): from then on the session has no sound. The terminal's
	// status row and the browser's status read it from other goroutines.
	audioOff atomic.Bool

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

	// Screenshot requests wait here for the video goroutine, which owns the
	// decoder and so is the only one that can scale its frame (requestShot).
	shotMu    sync.Mutex
	shotReqs  []chan videoShot
	shotEnded bool // runVideo has returned: no frame will come

	// shotDone is the last screenshot's channel, which its goroutine closes
	// once it is saved or dropped; until then that shot is in flight
	// (takeShot). Only the run loop reads or sets the field.
	shotDone chan struct{}
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

// audioTurnedOff reports whether the device has turned the audio stream off.
func (s *streamState) audioTurnedOff() bool {
	return s != nil && s.audioOff.Load()
}

// soundOn reports whether the session's sound is on: the session has a sound
// stream, and the device has not turned it off. The browser gets that sound
// whether or not this computer's sink opened.
func (s *streamState) soundOn() bool {
	return s != nil && sessionHasSound(s.cfg, s.sess) && !s.audioOff.Load()
}

type videoFrame struct {
	rgb  []byte
	w, h int // video frame dims (session size)
	cw   int // canvas dims (rgb length = cw*ch*4)
	ch   int
	fps  float64
	refs atomic.Int32 // the web sink's holders of rgb (see webServer.frame)
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
	s.fitW, s.fitH, s.fitOffX, s.fitOffY = fitPicture(s.canvasW, s.canvasH, s.videoW, s.videoH)
}

// fitPicture is where a fw x fh video picture goes in a cw x ch terminal
// canvas: its letterboxed size and offset. The screenshot fallback crops the
// canvas with it.
func fitPicture(cw, ch, fw, fh int) (w, h, offX, offY int) {
	if fw <= 0 || fh <= 0 {
		// nothing yet: fit is the full canvas
		return cw, ch, 0, 0
	}
	scaleX := float64(cw) / float64(fw)
	scaleY := float64(ch) / float64(fh)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}
	if scale > 1 {
		scale = 1 // never upscale past source (crisper + faster)
	}
	w = int(float64(fw) * scale)
	h = int(float64(fh) * scale)
	if w%2 == 1 {
		w--
	}
	if h%2 == 1 {
		h--
	}
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return w, h, (cw - w) / 2, (ch - h) / 2
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

// lostError is how a stream that has started ends when a read from the device
// fails: the device, or the connection to it, went away. When it ends the
// stream the display shows, scterm exits 2, as scrcpy does when the device
// disconnects. A stream that fails before it starts (declined, an unknown
// codec, no session header) ends with another error.
type lostError struct{ error }

func (e lostError) Unwrap() error { return e.error }

// errAudioOff is how runAudio ends when the device turns the audio stream off
// (codec id 0), which the server does when it cannot record the sound, as with
// playback capture before Android 13. The session goes on without sound, as in
// scrcpy.
var errAudioOff = errors.New("the device turned the audio stream off")

// runVideo demuxes the video socket: headers -> packets -> decoder.
func (s *streamState) runVideo() error {
	defer s.endShots()
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
	wake := func() {}
	r := bufio.NewReaderSize(shotReader{s.sess.video, &wake}, 1<<20)

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

	// A still screen sends no frames, so a screenshot request also wakes a
	// read that waits for the device, and gets the frame the decoder holds.
	var lastW, lastH int // its size, once scaled
	wake = func() {
		if lastW > 0 {
			s.answerShots(dec, lastW, lastH)
		}
	}

	fps := s.fpsCounter()

	for {
		if _, err := io.ReadFull(r, he); err != nil {
			return lostError{fmt.Errorf("stream ended: %w", err)}
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
			return lostError{fmt.Errorf("packet: %w", err)}
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
			fw, fh, err := vdecScaleStride(dec, reg, s.canvasW*4, s.fitW, s.fitH)
			if err != nil {
				s.returnPooled(buf)
				continue
			}
			lastW, lastH = fw, fh
			s.frameCount.Add(1)
			s.deliverFrame(&videoFrame{rgb: buf, w: s.videoW, h: s.videoH,
				cw: s.canvasW, ch: s.canvasH, fps: fps()})
			s.answerShots(dec, fw, fh)
		}
	}
}

// videoShot is the decoded frame scaled at its own size for a screenshot:
// BGR0 pixels (see writePPMFile), w x h, or the error that stopped the scale.
type videoShot struct {
	rgb  []byte
	w, h int
	err  error
}

// requestShot asks the video goroutine for the frame its decoder holds, at the
// frame's own size. The channel gets the picture, or is closed without one if
// the video stream ends first; nil means it has ended already. The read
// deadline makes a read that waits for the device return at once (see
// shotReader), so the answer does not wait for the next frame.
func (s *streamState) requestShot() chan videoShot {
	s.shotMu.Lock()
	if s.shotEnded {
		s.shotMu.Unlock()
		return nil
	}
	ch := make(chan videoShot, 1)
	s.shotReqs = append(s.shotReqs, ch)
	s.shotMu.Unlock()
	// The request must be registered before the deadline is set: the wake
	// that the deadline causes has to find it. Set first, the deadline could
	// wake the video goroutine, which clears it and finds no request, and the
	// request would wait for the next frame.
	if s.sess != nil && s.sess.video != nil {
		s.sess.video.SetReadDeadline(time.Now())
	}
	return ch
}

// cancelShot withdraws a request that is no longer waited for.
func (s *streamState) cancelShot(ch chan videoShot) {
	s.shotMu.Lock()
	defer s.shotMu.Unlock()
	if i := slices.Index(s.shotReqs, ch); i >= 0 {
		s.shotReqs = slices.Delete(s.shotReqs, i, i+1)
	}
}

// answerShots scales the decoder's frame, w x h, into a fresh buffer for the
// waiting requests. Only the video goroutine calls it: the decoder is its own.
func (s *streamState) answerShots(dec unsafe.Pointer, w, h int) {
	s.shotMu.Lock()
	reqs := s.shotReqs
	s.shotReqs = nil
	s.shotMu.Unlock()
	if len(reqs) == 0 {
		return
	}
	shot := videoShot{rgb: make([]byte, w*h*4), w: w, h: h}
	fw, fh, err := vdecScaleStride(dec, shot.rgb, w*4, w, h)
	if err == nil && (fw != w || fh != h) {
		// The frame is not the size last scaled: scale it at its own.
		shot = videoShot{rgb: make([]byte, fw*fh*4), w: fw, h: fh}
		_, _, err = vdecScaleStride(dec, shot.rgb, fw*4, fw, fh)
	}
	if err != nil {
		shot = videoShot{err: err}
	}
	for _, ch := range reqs {
		ch <- shot // each channel has room for its one answer
	}
}

// endShots tells the waiting requests that no frame will come.
func (s *streamState) endShots() {
	s.shotMu.Lock()
	defer s.shotMu.Unlock()
	s.shotEnded = true
	for _, ch := range s.shotReqs {
		close(ch)
	}
	s.shotReqs = nil
}

// shotReader is the video socket as runVideo reads it. A read that ends at
// requestShot's deadline lets the video goroutine answer the request (wake),
// and reads on; every other result passes through as it came. Every read
// deadline that expires on the video socket is taken for such a wake and
// cleared, so a deadline set there for another reason, such as a liveness
// timeout, would be silently defeated.
type shotReader struct {
	conn net.Conn
	wake *func()
}

func (r shotReader) Read(p []byte) (int, error) {
	for {
		n, err := r.conn.Read(p)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return n, err
		}
		// Cleared before the wake looks for requests: a request made after
		// that look sets the deadline again, and the next read answers it.
		// Cleared after the look, that request's deadline could be cleared
		// too, and it would wait for the next frame.
		r.conn.SetReadDeadline(time.Time{})
		(*r.wake)()
		if n > 0 {
			return n, nil
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
		s.audioOff.Store(true)
		return errAudioOff
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
				return lostError{fmt.Errorf("audio raw: %w", err)}
			}
			_, ptsUs, _, _, size := parsePacketHeader(he)
			if size == 0 {
				continue
			}
			s.packets++
			s.lastAudioPts.Store(ptsUs)
			payload := make([]byte, size)
			if _, err := io.ReadFull(r, payload); err != nil {
				return lostError{fmt.Errorf("audio raw packet: %w", err)}
			}
			if s.cfg.audioDump != "" {
				// before writePCM16, which scales the samples in place
				dumpAudioPacket(s.cfg.audioDump, payload)
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
			return lostError{fmt.Errorf("audio stream ended: %w", err)}
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
			return lostError{fmt.Errorf("audio packet: %w", err)}
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
			dumpAudioPacket(s.cfg.audioDump, payload)
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
