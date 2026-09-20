package main

// Web display mode.
//
// --web / --window run the exact same pipeline as the TUI (same adb tunnel,
// same scrcpy server, same libavcodec decode, same control socket) but hand
// the decoded canvas to a browser instead of to terminal cells:
//
//	device -> h264 --libavcodec--> BGR0 canvas --mjpeg--> browsers
//	                                      |
//	                            (per client, drop-old mailbox)
//
// Design rules, in priority order:
//
//  1. Never slow down the input path. Control events are parsed in the socket
//     reader and applied by one dispatcher goroutine; they never wait for a
//     frame to be encoded or for a slow browser.
//  2. Never make the demux loop wait. Every client owns a one-slot mailbox:
//     a slow client loses frames (drop-old) instead of applying backpressure
//     to the decoder, which is what keeps latency flat when a browser stalls.
//  3. Never re-decode. One h264 decoder feeds the canvas; browsers receive
//     JPEG stills (hardware-decoded by the browser, ~0.1ms at 480x1080).

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// wire protocol
// ---------------------------------------------------------------------------

// Binary (server -> client):
//
//	0x01 video : u32 seq, u16 w, u16 h, u16 fpsx10, u8 format, u8 flags, 8B pad,
//	             then the frame: JPEG bytes, or w*h*4 BGR0 bytes when raw
//	0x02 audio : PCM s16le stereo 48 kHz
//	0x03 hello : JSON (server capabilities, sent right after the client hello)
//
// Text (server -> client): JSON status/ack messages.
// Text (client -> server): JSON control events, see webEvent.
const (
	webMsgVideo = 0x01
	webMsgAudio = 0x02
	webMsgHello = 0x03

	// webHeaderLen is the fixed video header size (1-byte magic included).
	webHeaderLen = 16

	// video payload formats
	webFormatJPEG = 0
	webFormatRaw  = 1 // BGR0, w*h*4 bytes

	// webDefaultQuality is the mjpeg qscale used when -web-quality is unset:
	// lower = better. 5 measures ~50 KB/frame at 480x1080 and ~2 ms to encode,
	// which is the sweet spot for 60 fps over localhost.
	webDefaultQuality = 5

	// webMinQuality/webMaxQuality bound the qscale (ffmpeg's range for mjpeg).
	webMinQuality = 2
	webMaxQuality = 31

	// Mailboxes are 1-deep on purpose: frame N+1 supersedes frame N.
	webAudioQueue = 48 // ~256 ms of 5 ms PCM bursts
)

// webCmdInbox is the depth of the control-event queue (see webInbox).

// webEvent is one control event from a client.
type webEvent struct {
	Op string `json:"op"`

	// pointer/keyboard
	X      uint32 `json:"x,omitempty"` // 0..65535, fraction of the canvas width
	Y      uint32 `json:"y,omitempty"` // 0..65535, fraction of the canvas height
	Btn    int    `json:"btn,omitempty"`
	Action string `json:"action,omitempty"` // down | move | up
	Code   uint32 `json:"code,omitempty"`   // Android keycode
	Meta   uint32 `json:"meta,omitempty"`   // Android metastate
	Text   string `json:"text,omitempty"`
	Delta  int    `json:"delta,omitempty"`  // wheel notches, positive = up
	Gain   int    `json:"gain,omitempty"`   // percent, -1 = no change
	Mute   *bool  `json:"mute,omitempty"`   // toggle/pin the audio gain
	W      int    `json:"w,omitempty"`      // viewport px (hello)
	H      int    `json:"h,omitempty"`      // viewport px (hello)
	Format string `json:"format,omitempty"` // auto | jpeg | raw (hello)
	Caps   string `json:"caps,omitempty"`   // "audio" when the client wants PCM

	// stats (client -> server telemetry, logged when SCT_DEBUG_WEB is set)
	Frames   int64   `json:"frames,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	DecodeMs float64 `json:"decodeMs,omitempty"`
	Errors   int64   `json:"errors,omitempty"`
	KBps     float64 `json:"kbps,omitempty"`
	Lost     int64   `json:"lost,omitempty"`
	Hidden   bool    `json:"hidden,omitempty"`
}

// webCanvas is the video-to-canvas mapping shared by the scaler, the status
// message and the pointer mapper, so all three always agree.
type webCanvas struct {
	cw, ch int // canvas size: what gets encoded
	vw, vh int // device video size
	fw, fh int // fitted (letterboxed) region inside the canvas
	ox, oy int // fitted region offset inside the canvas
	gen    uint64
}

func (c webCanvas) valid() bool { return c.cw > 1 && c.ch > 1 }

// computeWebCanvas fits a video of vw x vh into a viewport of tw x th pixels.
// Unlike the TUI canvas (1 pixel per column, 2 per row) this is square-pixel,
// because the browser draws it 1:1.
func computeWebCanvas(tw, th, vw, vh int) webCanvas {
	if tw < 2 {
		tw = 2
	}
	if th < 2 {
		th = 2
	}
	c := webCanvas{cw: tw, ch: th, vw: vw, vh: vh}
	if vw <= 0 || vh <= 0 {
		c.fw, c.fh = tw, th
		return c
	}
	scale := float64(tw) / float64(vw)
	if s := float64(th) / float64(vh); s < scale {
		scale = s
	}
	if scale > 1 {
		scale = 1 // never upscale the source: crisper and cheaper
	}
	fw := int(float64(vw) * scale)
	fh := int(float64(vh) * scale)
	fw &^= 1 // JPEG 4:2:0 needs even dimensions
	fh &^= 1
	if fw < 2 {
		fw = 2
	}
	if fh < 2 {
		fh = 2
	}
	c.fw, c.fh = fw, fh
	c.ox = (tw - fw) / 2
	c.oy = (th - fh) / 2
	return c
}

// videoAt maps normalized client coordinates onto the device video space.
// The returned position carries the device size, which the scrcpy server
// requires to match or it drops the event.
func (c webCanvas) videoAt(nx, ny uint32) position {
	if c.vw <= 0 || c.vh <= 0 {
		return position{}
	}
	px := int(float64(nx) / 65535.0 * float64(c.cw))
	py := int(float64(ny) / 65535.0 * float64(c.ch))
	x := int32(float64(px-c.ox) / float64(c.fw) * float64(c.vw))
	y := int32(float64(py-c.oy) / float64(c.fh) * float64(c.vh))
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if int(x) >= c.vw {
		x = int32(c.vw - 1)
	}
	if int(y) >= c.vh {
		y = int32(c.vh - 1)
	}
	return position{x: x, y: y, screenW: uint16(c.vw), screenH: uint16(c.vh)}
}

// ---------------------------------------------------------------------------
// clients
// ---------------------------------------------------------------------------

type webClient struct {
	srv *webServer
	ws  *wsConn
	id  uint64

	// last requested viewport (CSS pixels x devicePixelRatio)
	tw, th int

	// negotiated capabilities (hello)
	cfgMu   sync.Mutex
	raw     bool // client asked for BGR0 instead of JPEG
	audioOn bool // client wants PCM

	mu   sync.Mutex
	cv   webCanvas
	hadV bool // a canvas exists (client has been told the geometry)

	frameMu    sync.Mutex
	frame      *videoFrame
	frameReady chan struct{}

	audioMu    sync.Mutex
	audio      [][]byte
	audioBytes int

	// outbound JSON: serialized by the sender goroutine only
	statusEvery time.Duration

	closed  atomic.Bool
	closeCh chan struct{}

	// last client-reported telemetry (see the "stats" event)
	statsFrame    atomic.Int64
	statsPaintFPS atomic.Int64
	statsErrors   atomic.Int64

	sentVideo, dropVideo atomic.Int64
	encErrors            atomic.Int64
}

// ---------------------------------------------------------------------------
// server
// ---------------------------------------------------------------------------

type webServer struct {
	cfg      config
	port     int
	addr     string
	stream   *streamState
	audio    *audioSink
	sess     *session
	ctrl     *controller
	app      *app // nil in headless web mode (--no-tui with --web)
	headless bool

	ln   net.Listener
	http *http.Server

	clientsMu sync.Mutex
	clients   []*webClient
	nextID    uint64

	// frame pool: slots returned by clients as soon as they are encoded/sent
	poolMu sync.Mutex
	pool   [4][]byte

	// encMu guards the encoder pool. encUse is a reader/writer barrier: every
	// encode happens under a read lock and every free (shutdown, reaping)
	// under the write lock, so an encoder can never be destroyed while a
	// client goroutine is still inside it.
	encMu  sync.Mutex
	enc    map[uint64]*jpegEncoder
	encUse sync.RWMutex

	inbox chan webCmd

	// ready is closed once the listener is up (and port/url are final), so
	// callers never have to poll for them.
	ready   chan struct{}
	url     string
	closing atomic.Bool
	done    chan struct{}

	nClients atomic.Int64
	// hadClient is true once any viewer has connected, ever. It is what tells
	// a browser that died before it could display anything (a failure worth
	// reporting) from one the user simply closed.
	hadClient atomic.Bool
	frames    atomic.Int64

	// win is the browser window launched for --window (nil otherwise), so
	// shutdown can close it instead of leaving an orphaned window behind.
	winMu sync.Mutex
	win   *exec.Cmd
	// winLog is the file the browser's output is captured to, kept only when it
	// might explain a failure.
	winLog string

	// encMsBits is an EWMA of the JPEG encode cost in milliseconds (float64
	// bits). Reported in the status message so the per-frame display cost is
	// observable instead of folklore.
	encMsBits atomic.Uint64

	dropLog atomic.Int64 // last unix-nano of a rate-limited error log

	// The DEVICE's own media volume, read back from the device (see
	// pollVolume). The browser HUD shows this instead of the host-sink gain:
	// that gain is applied in writePCM16, which feeds the PulseAudio sink, while
	// browsers are fed raw PCM by pushPCM. So the gain could never change what a
	// browser viewer hears, and the HUD was reporting a number that did nothing.
	// devVolIdx < 0 means "no reading yet".
	devVolIdx atomic.Int32
	devVolMax atomic.Int32
	volKick   chan struct{} // depth 1: coalesces refresh requests
}

// noteEncodeTime folds one measured encode into the EWMA (alpha = 1/8, so a
// geometry or quality change shows up within a few frames).
func (s *webServer) noteEncodeTime(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	for {
		old := s.encMsBits.Load()
		next := ms
		if old != 0 {
			next = math.Float64frombits(old)*0.875 + ms*0.125
		}
		if s.encMsBits.CompareAndSwap(old, math.Float64bits(next)) {
			return
		}
	}
}

// encodeMs returns the smoothed per-frame encode cost in milliseconds.
func (s *webServer) encodeMs() float64 {
	if v := s.encMsBits.Load(); v != 0 {
		return math.Float64frombits(v)
	}
	return 0
}

// ---------------------------------------------------------------------------
// device media volume
// ---------------------------------------------------------------------------

// devVolStream is STREAM_MUSIC: the stream an app's own audio plays on, which
// is what "the app's volume" means to a viewer.
const devVolStream = "3"

// readDeviceVolume asks the device for its media volume.
//
// `cmd media_session volume --get` prints "volume is <idx> in range [<lo>..<hi>]"
// on stdout and hands back the index and the device's own scale in one call.
// That matters because Android's scale is not fixed (15 on this device, but the
// ceiling varies by device and profile), so a bare index is meaningless without
// it. `settings get system volume_music_speaker` was tried first and is stale,
// and `dumpsys audio`'s layout drifts between releases.
//
// ok=false means the device did not answer or the output did not parse; callers
// keep the previous reading rather than flashing a bogus 0% (which a viewer
// would read as "muted").
func (s *webServer) readDeviceVolume() (idx, max int, ok bool) {
	if s.sess == nil || s.sess.adb == nil {
		return 0, 0, false
	}
	out, err := s.sess.adb.run("shell", "cmd", "media_session", "volume",
		"--stream", devVolStream, "--get")
	if err != nil {
		s.logLimited("scterm: web: device volume: %v\n", err)
		return 0, 0, false
	}
	return parseDeviceVolume(out)
}

// parseDeviceVolume pulls the index and scale out of `cmd media_session volume
// --get` output. The command prefixes every line with a log tag ("[V] "), so
// the parse starts at the "volume is" substring rather than at the beginning.
func parseDeviceVolume(out []byte) (idx, max int, ok bool) {
	txt := string(out)
	i := strings.Index(txt, "volume is ")
	if i < 0 {
		return 0, 0, false
	}
	var v, lo, hi int
	if _, err := fmt.Sscanf(txt[i:], "volume is %d in range [%d..%d]", &v, &lo, &hi); err != nil {
		return 0, 0, false
	}
	if hi <= lo || v < 0 || v > hi {
		return 0, 0, false
	}
	return v, hi, true
}

// kickVolume asks pollVolume to re-read the device volume now, so the HUD does
// not lag the device by a whole poll interval after a volume key. Non-blocking:
// a pending refresh already covers this request.
func (s *webServer) kickVolume() {
	select {
	case s.volKick <- struct{}{}:
	default:
	}
}

// pollVolume keeps devVolIdx/devVolMax in sync with the device.
//
// This deliberately does not touch either hot path: input is applied by the
// dispatcher goroutine and frames are encoded by the client goroutines, so an
// adb round trip here (~50ms) can never delay a touch or a frame. It also stays
// completely idle while no browser is connected, since nothing displays it.
func (s *webServer) pollVolume() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if s.nClients.Load() == 0 {
				continue // nobody is watching the value
			}
		case <-s.volKick:
			// A volume key was just injected: let the device apply it first,
			// otherwise we read the pre-key value straight back.
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.done:
				return
			}
		}
		if idx, max, ok := s.readDeviceVolume(); ok {
			s.devVolIdx.Store(int32(idx))
			s.devVolMax.Store(int32(max))
		}
	}
}

// deviceVolume reports the last known media volume as a percentage of the
// device's own scale. ok=false means no reading has succeeded yet.
func (s *webServer) deviceVolume() (pct, idx, max int, ok bool) {
	i := s.devVolIdx.Load()
	m := s.devVolMax.Load()
	if i < 0 || m <= 0 {
		return 0, 0, 0, false
	}
	return int(int64(i) * 100 / int64(m)), int(i), int(m), true
}

// dbgWeb turns on per-client telemetry logging (SCT_DEBUG_WEB=1), including
// the browser's own paint/decode counters.
var dbgWeb = os.Getenv("SCT_DEBUG_WEB") != ""

// debugNoPool disables buffer recycling (test knob for ownership bugs).
var debugNoPool = false

// jpegEncoder is a pooled native mjpeg encoder pinned to one canvas geometry.
//
// mu serialises use of the underlying AVCodecContext. The pool hands out ONE
// encoder per geometry, and since every web client now shares the canvas (the
// video's own geometry), several client goroutines can reach the same encoder
// at once. A libavcodec context is not thread-safe, and the Go race detector
// cannot see a race that lives inside C: without mu this faulted inside
// sct_jenc_encode_to (SIGSEGV, addr=0x0) even with a correctly sized buffer.
// See TestEncodeWithConcurrentClients.
type jpegEncoder struct {
	mu      sync.Mutex // guards h: one encode at a time
	h       *jencHandle
	w, hgt  int
	maxSize int
	lastUse int64
}

func newWebServer(cfg config, sess *session, stream *streamState, audio *audioSink, ctrl *controller, a *app) *webServer {
	s := &webServer{
		cfg:      cfg,
		port:     cfg.webPort,
		addr:     cfg.webAddr,
		stream:   stream,
		audio:    audio,
		sess:     sess,
		ctrl:     ctrl,
		app:      a,
		headless: a == nil,
		enc:      make(map[uint64]*jpegEncoder),
		inbox:    make(chan webCmd, webInbox),
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
		volKick:  make(chan struct{}, 1),
	}
	s.devVolIdx.Store(-1) // no reading yet; 0 would mean "device muted"
	return s
}

// run starts the HTTP server, prints the URL, optionally opens a browser
// window, and blocks until the server is shut down.
func (s *webServer) run(openWindow bool) error {
	host := s.addr
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", s.port)))
	if err != nil {
		return fmt.Errorf("web: listen on %s:%d: %w", host, s.port, err)
	}
	s.ln = ln
	// Record the real port (0 = pick any free port).
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		s.port = ta.Port
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/player.js", s.handleJS)
	mux.HandleFunc("/pointer.js", s.handlePointerJS)
	mux.HandleFunc("/player.css", s.handleCSS)
	mux.HandleFunc("/pcm-worklet.js", s.handleWorklet)
	mux.HandleFunc("/ws", s.handleWS)

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: /ws hijacks the connection and owns it afterwards.
	}

	url := fmt.Sprintf("http://%s/", net.JoinHostPort(host, fmt.Sprintf("%d", s.port)))
	if host == "0.0.0.0" || host == "::" {
		url = fmt.Sprintf("http://<this-host>:%d/", s.port)
	}
	s.url = url
	close(s.ready)
	if s.audio == nil || s.audio.err != nil {
		fmt.Fprintf(stderrWriter(), "scterm: web display on %s (video only: %s)\n", url, s.audioErrString())
	} else {
		fmt.Fprintf(stderrWriter(), "scterm: web display on %s\n", url)
	}

	// The PCM tap is registered before the first client connects: it fans out
	// to whoever is listening at the time.
	s.stream.addPCMTap(s.pushPCM)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.http.Serve(ln) }()

	// One goroutine applies control events in order; see webinput.go.
	go s.runDispatcher()

	// Keep the app's status line (TUI mode) honest, and log a heartbeat for
	// headless runs; also reap stale encoders.
	go s.maintenance()

	// Track the device's media volume for the browser HUD, off every hot path.
	go s.pollVolume()

	if openWindow {
		if err := s.openWindow(url); err != nil {
			fmt.Fprintf(stderrWriter(), "scterm: could not open a window: %v\n", err)
			fmt.Fprintf(stderrWriter(), "scterm: open %s manually\n", url)
		}
	}

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("web: %w", err)
		}
	case <-s.done:
	}
	return nil
}

// stop shuts the server down and disconnects every client.
func (s *webServer) stop() {
	if s.closing.Swap(true) {
		return
	}
	close(s.done)
	if s.http != nil {
		ctx, cancel := timeoutContext(2 * time.Second)
		_ = s.http.Shutdown(ctx)
		cancel()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.clientsMu.Lock()
	clients := append([]*webClient(nil), s.clients...)
	s.clientsMu.Unlock()
	for _, c := range clients {
		c.close()
	}
	// Close the window we opened: it displays nothing without the server, and
	// an orphan would hold the private profile lock against the next run.
	s.killWindow()
	s.freeEncoders()
}

func (s *webServer) audioErrString() string {
	if s.audio == nil {
		return "audio disabled"
	}
	if s.audio.err == nil {
		return "audio ok"
	}
	return s.audio.errString()
}

// frame is the frameSink implementation: called from the video demux goroutine
// for every decoded frame. It hands each client the newest frame and never
// blocks (a client whose mailbox is still full loses its oldest frame).
//
// Buffer ownership, which is what keeps this allocation-free and race-free:
// the sink owns the buffer until a client queues it in its mailbox; the
// client's sender owns it until it has encoded/written it; whoever removes a
// frame from a mailbox returns its buffer to the pool exactly once.
func (s *webServer) frame(f *videoFrame) {
	if f == nil {
		return
	}
	s.frames.Add(1)
	s.clientsMu.Lock()
	clients := s.clients
	s.clientsMu.Unlock()

	queued := false
	for _, c := range clients {
		if c.closed.Load() {
			continue
		}
		if c.offer(f) {
			queued = true
		}
	}
	if !queued {
		s.poolPut(f.rgb)
	}
}

// offer hands one frame to a client. It reports whether the frame is now owned
// by that client's mailbox (false: the sink must recycle it).
func (c *webClient) offer(f *videoFrame) bool {
	if c.closed.Load() {
		return false
	}
	c.frameMu.Lock()
	if c.closed.Load() {
		c.frameMu.Unlock()
		return false
	}
	old := c.frame
	c.frame = f
	c.frameMu.Unlock()
	if old != nil {
		// We just took it out of the mailbox, so we own it now.
		c.srv.poolPut(old.rgb)
	}
	select {
	case c.frameReady <- struct{}{}:
	default:
	}
	return true
}

// takeFrame returns the newest frame (the caller then owns its buffer).
func (c *webClient) takeFrame() *videoFrame {
	c.frameMu.Lock()
	f := c.frame
	c.frame = nil
	c.frameMu.Unlock()
	return f
}

// pushPCM is the stream PCM tap: fan out to clients that want audio.
func (s *webServer) pushPCM(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	s.clientsMu.Lock()
	clients := s.clients
	s.clientsMu.Unlock()
	if len(clients) == 0 {
		return
	}
	for _, c := range clients {
		if c.closed.Load() || !c.wantsAudio() {
			continue
		}
		// Copy: the demux loop reuses its buffer as soon as we return.
		dup := make([]byte, len(pcm))
		copy(dup, pcm)
		c.audioMu.Lock()
		if len(c.audio) >= webAudioQueue {
			// The client is behind: drop the oldest burst rather than grow.
			// 48 queued bursts is already ~256 ms of audio; anything older is
			// worthless for lip sync.
			c.audio = c.audio[1:]
		}
		c.audio = append(c.audio, dup)
		c.audioBytes += len(pcm)
		c.audioMu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// frame pool (the encoder may be slower than the network, or vice versa)
// ---------------------------------------------------------------------------

func (s *webServer) poolGet(n int) []byte {
	if debugNoPool {
		return make([]byte, n)
	}
	s.poolMu.Lock()
	for i, b := range s.pool {
		if b != nil && cap(b) >= n {
			s.pool[i] = nil
			s.poolMu.Unlock()
			return b[:n]
		}
	}
	s.poolMu.Unlock()
	b := make([]byte, n)
	return b
}

func (s *webServer) poolPut(b []byte) {
	if b == nil || debugNoPool {
		return
	}
	s.poolMu.Lock()
	for i := range s.pool {
		if s.pool[i] == nil {
			s.pool[i] = b
			s.poolMu.Unlock()
			return
		}
	}
	s.poolMu.Unlock()
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func webNoCache(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Pragma", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
}

func (s *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	webNoCache(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Inline script/style only; connect-src for the WebSocket. img-src blob:
	// is used by the screenshot key.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
			"connect-src 'self' ws: wss:; img-src 'self' blob: data:; media-src 'none'; worker-src 'self' blob:")
	_, _ = w.Write([]byte(webIndexHTML))
}

func (s *webServer) handleJS(w http.ResponseWriter, r *http.Request) {
	webNoCache(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(webPlayerJS))
}

func (s *webServer) handlePointerJS(w http.ResponseWriter, r *http.Request) {
	webNoCache(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(webPointerJS))
}

func (s *webServer) handleCSS(w http.ResponseWriter, r *http.Request) {
	webNoCache(w)
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(webPlayerCSS))
}

func (s *webServer) handleWorklet(w http.ResponseWriter, r *http.Request) {
	webNoCache(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(webPCMWorkletJS))
}

// originOK rejects cross-origin WebSocket handshakes (a random page must not
// be able to drive the device through this server).
func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client (curl, tests, a native shell)
	}
	host := r.Host
	trimmed := origin
	if i := strings.Index(trimmed, "://"); i >= 0 {
		trimmed = trimmed[i+3:]
	}
	if i := strings.IndexAny(trimmed, "/"); i >= 0 {
		trimmed = trimmed[:i]
	}
	return strings.EqualFold(trimmed, host)
}

func (s *webServer) handleWS(w http.ResponseWriter, r *http.Request) {
	if !wsIsUpgrade(r) {
		http.Error(w, "expected a websocket upgrade", http.StatusBadRequest)
		return
	}
	if !originOK(r) {
		http.Error(w, "cross-origin websocket refused", http.StatusForbidden)
		return
	}
	ws, err := wsUpgrade(w, r)
	if err != nil {
		fmt.Fprintf(stderrWriter(), "scterm: web: handshake: %v\n", err)
		return
	}

	s.clientsMu.Lock()
	s.nextID++
	c := &webClient{
		srv:         s,
		ws:          ws,
		id:          s.nextID,
		tw:          480,
		th:          1080,
		frameReady:  make(chan struct{}, 1),
		closeCh:     make(chan struct{}),
		statusEvery: 500 * time.Millisecond,
	}
	s.clients = append(s.clients, c)
	n := len(s.clients)
	s.clientsMu.Unlock()
	s.nClients.Store(int64(n))
	s.hadClient.Store(true)

	logOnce(fmt.Sprintf("scterm: web client %d connected (%d total)\n", c.id, n))
	c.sendStatus()

	go c.sendLoop()
	go c.recvLoop()
}

// ---------------------------------------------------------------------------
// client loops
// ---------------------------------------------------------------------------

func (c *webClient) close() {
	if c.closed.Swap(true) {
		return
	}
	close(c.closeCh)
	_ = c.ws.Close()
	c.frameMu.Lock()
	if c.frame != nil {
		c.srv.poolPut(c.frame.rgb)
		c.frame = nil
	}
	c.frameMu.Unlock()
	c.audioMu.Lock()
	c.audio = nil
	c.audioMu.Unlock()

	s := c.srv
	s.clientsMu.Lock()
	for i, x := range s.clients {
		if x == c {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			break
		}
	}
	n := len(s.clients)
	s.clientsMu.Unlock()
	s.nClients.Store(int64(n))
	logOnce(fmt.Sprintf("scterm: web client %d disconnected (%d left)\n", c.id, n))

	// Window mode: the window *is* the session. When its last viewer goes
	// away there is nothing left to display, so stop.
	if s.cfg.window && n == 0 && !s.closing.Load() {
		s.quit()
	}
}

func (s *webServer) quit() {
	if s.app != nil {
		select {
		case s.app.events <- inputEvent{kind: evQuit}:
		default:
		}
		return
	}
	s.stop()
}

// sendLoop owns every write to the socket: video frames, PCM and status JSON.
func (c *webClient) sendLoop() {
	defer c.close()

	ticker := time.NewTicker(c.statusEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.closeCh:
			return
		case <-c.frameReady:
			f := c.takeFrame()
			if f == nil {
				continue
			}
			c.sendFrame(f)
		case <-ticker.C:
			c.sendStatus()
			c.drainAudio()
		}
	}
}

// sendFrame encodes (when needed) and writes one video frame.
//
// The JPEG is encoded *directly* into the pooled buffer at the frame header
// offset, so a frame goes out with exactly one allocation and one write:
// [webHeaderLen header][JPEG or BGR0].
func (c *webClient) sendFrame(f *videoFrame) {
	cv := c.canvasFor(f)
	c.cfgMu.Lock()
	raw := c.raw
	c.cfgMu.Unlock()

	s := c.srv

	// The frame's buffer is the only thing that can be encoded, and its
	// geometry is f.cw x f.ch (videoFrame's contract: len(rgb) == cw*ch*4).
	// Trusting a client-derived canvas instead of the frame's is what let the
	// native encoder read megabytes past a shorter buffer; never encode a
	// canvas the buffer cannot fill.
	if need := cv.cw * cv.ch * 4; need <= 0 || len(f.rgb) < need {
		c.encErrors.Add(1)
		s.logLimited("scterm: web: frame buffer %d bytes cannot fill canvas %dx%d (%d bytes)\n",
			len(f.rgb), cv.cw, cv.ch, need)
		s.poolPut(f.rgb)
		return
	}

	var buf []byte
	var format byte
	if raw {
		format = webFormatRaw
		buf = s.poolGet(webHeaderLen + len(f.rgb))
		copy(buf[webHeaderLen:], f.rgb)
		s.poolPut(f.rgb)
	} else {
		format = webFormatJPEG
		size := s.encoderMaxSize(cv)
		if size <= 0 {
			s.poolPut(f.rgb)
			return
		}
		buf = s.poolGet(webHeaderLen + size)
		// encodeWith records the encode cost itself (inside the encoder lock).
		n, err := s.encodeWith(cv, f.rgb, buf, webHeaderLen)
		s.poolPut(f.rgb)
		if err != nil || n <= 0 {
			if err != errJencTooSmall {
				c.encErrors.Add(1)
				s.logLimited("scterm: web: jpeg encode: %v\n", err)
			}
			s.poolPut(buf)
			return
		}
		buf = buf[:webHeaderLen+n]
	}

	seq := uint32(c.sentVideo.Add(1))
	buf[0] = webMsgVideo
	binary.BigEndian.PutUint32(buf[1:5], seq)
	binary.BigEndian.PutUint16(buf[5:7], uint16(cv.cw))
	binary.BigEndian.PutUint16(buf[7:9], uint16(cv.ch))
	fps10 := uint16(f.fps * 10)
	if f.fps*10 > 65535 {
		fps10 = 65535
	}
	binary.BigEndian.PutUint16(buf[9:11], fps10)
	buf[11] = format
	for i := 12; i < webHeaderLen; i++ {
		buf[i] = 0
	}

	if err := c.ws.WriteBinary(buf); err != nil {
		s.poolPut(buf)
		return
	}
	s.poolPut(buf)

	// Ship whatever PCM accumulated while this frame was being encoded, so
	// audio and video stay together instead of racing for the socket.
	c.drainAudio()
}

// canvasFor returns the canvas geometry for a frame.
//
// The canvas is the FRAME's geometry, never the client's viewport. One frame
// buffer is rendered per frame and shared by every client, so a client that
// picked its own canvas would either read past that buffer (SIGSEGV in the
// native encoder -- this was a real crash) or need a per-client rescale on the
// host. The browser instead scales the canvas to its viewport with CSS, which
// costs nothing and loses nothing, because the canvas is already the video's
// full resolution. c.tw/c.th therefore only affect how the page displays the
// canvas, not how the host renders it.
func (c *webClient) canvasFor(f *videoFrame) webCanvas {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hadV || c.cv.cw != f.cw || c.cv.ch != f.ch || c.cv.vw != f.w || c.cv.vh != f.h {
		c.cv = computeWebCanvas(f.cw, f.ch, f.w, f.h)
		// The status message reports the device size from streamState, which
		// only the demux goroutine knows; the canvas is the cheaper, always
		// available source of truth for "has this client seen a frame yet".
		c.hadV = true
	}
	return c.cv
}

// drainAudio writes all queued PCM bursts as one coalesced message.
func (c *webClient) drainAudio() {
	c.audioMu.Lock()
	if len(c.audio) == 0 {
		c.audioMu.Unlock()
		return
	}
	total := 0
	for _, b := range c.audio {
		total += len(b)
	}
	out := make([]byte, 1+total)
	out[0] = webMsgAudio
	off := 1
	for _, b := range c.audio {
		copy(out[off:], b)
		off += len(b)
	}
	c.audio = c.audio[:0]
	c.audioMu.Unlock()
	_ = c.ws.WriteBinary(out)
}

// wantAudio reports whether this client asked for PCM.
func (c *webClient) wantsAudio() bool {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.audioOn
}

// devicePos maps a client pointer event onto the device video space using the
// geometry of the canvas that client was last sent.
//
// Availability: input from a browser is only meaningful after it has received a
// frame header, which is exactly when the canvas is recorded (canvasFor runs in
// the client's sender goroutine before the first frame goes out). A pointer
// event that arrives before that maps to 0,0 instead of inventing geometry.
func (c *webClient) devicePos(ev *webEvent) position {
	if c == nil {
		return position{}
	}
	c.mu.Lock()
	ready := c.hadV
	cv := c.cv
	c.mu.Unlock()
	if !ready {
		return position{}
	}
	return cv.videoAt(ev.X, ev.Y)
}

// recvLoop reads control events and queues them for the dispatcher.
func (c *webClient) recvLoop() {
	for {
		op, data, err := c.ws.ReadMessage()
		if err != nil {
			c.close()
			return
		}
		if op != wsOpText {
			continue
		}
		var ev webEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		if !c.applyEvent(&ev) {
			c.srv.dispatch(c, &ev)
		}
	}
}

// applyEvent handles connection-level events inline (they must not queue
// behind a burst of input). Returns true when the event is fully handled.
func (c *webClient) applyEvent(ev *webEvent) bool {
	switch ev.Op {
	case "hello":
		c.mu.Lock()
		if ev.W >= 2 && ev.H >= 2 && ev.W <= 16384 && ev.H <= 16384 {
			c.tw, c.th = ev.W, ev.H
		}
		c.cv = webCanvas{}
		c.hadV = false
		c.mu.Unlock()
		c.cfgMu.Lock()
		c.raw = strings.EqualFold(ev.Format, "raw")
		c.audioOn = ev.Caps == "" || strings.Contains(ev.Caps, "audio")
		c.cfgMu.Unlock()
		c.sendHello()
		return true
	case "resize":
		c.mu.Lock()
		if ev.W >= 2 && ev.H >= 2 && ev.W <= 16384 && ev.H <= 16384 {
			c.tw, c.th = ev.W, ev.H
		}
		c.mu.Unlock()
		return true
	case "ping":
		c.sendStatus()
		return true
	case "stats":
		// Client-side health: only a browser knows whether frames are really
		// being painted, so this is the one place JS reports upward.
		if dbgWeb {
			fmt.Fprintf(stderrWriter(),
				"scterm: web client %d: %d painted, %.1f paint fps, %.2f ms decode, %.2f ms encode, %d errors, %d lost, %.0f kbps, hidden=%v\n",
				c.id, ev.Frames, ev.FPS, ev.DecodeMs, c.srv.encodeMs(), ev.Errors, ev.Lost, ev.KBps, ev.Hidden)
		}
		c.statsFrame.Store(ev.Frames)
		c.statsPaintFPS.Store(int64(ev.FPS * 10))
		c.statsErrors.Store(ev.Errors)
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// outbound JSON
// ---------------------------------------------------------------------------

type webHello struct {
	Op      string `json:"op"`
	Proto   int    `json:"proto"`
	Device  string `json:"device"`
	VideoW  int    `json:"videoW"`
	VideoH  int    `json:"videoH"`
	Audio   bool   `json:"audio"`
	Raw     bool   `json:"raw"`
	Version string `json:"version"`
}

type webStatus struct {
	Op      string  `json:"op"`
	FPS     float64 `json:"fps"`
	VideoW  int     `json:"videoW"`
	VideoH  int     `json:"videoH"`
	CanvasW int     `json:"canvasW"`
	CanvasH int     `json:"canvasH"`
	FitW    int     `json:"fitW"`
	FitH    int     `json:"fitH"`
	Gain    int     `json:"gain"`
	Muted   bool    `json:"muted"`
	Audio   string  `json:"audio"`
	// DevVol is the DEVICE's media volume as a percentage of its own scale
	// (DevVolIdx out of DevVolMax). This is what the HUD shows: it is the
	// volume the volume keys actually change, and — unlike Gain — it is the
	// only thing that changes what a browser viewer hears. DevVolOK is false
	// until a reading succeeds, so the UI can say "—" instead of a fake 0%.
	DevVol    int     `json:"devVol"`
	DevVolIdx int     `json:"devVolIdx"`
	DevVolMax int     `json:"devVolMax"`
	DevVolOK  bool    `json:"devVolOk"`
	Clients   int64   `json:"clients"`
	Sent      int64   `json:"sent"`
	Dropped   int64   `json:"dropped"`
	Control   bool    `json:"control"`
	Keys      int     `json:"keys"` // Android keycodes the UI can send
	KBps      float64 `json:"kbps"`
	JPEGms    float64 `json:"jpegMs"`
	RawBytes  int64   `json:"rawBytes"`
	// AudioPeak is the largest |sample| seen since start (0 = the device is
	// sending digital silence). The TUI status line has always shown this; web
	// mode needs it to answer "is the device actually making a sound?" without
	// listening.
	AudioPeak int16 `json:"audioPeak"`
	// HostAudio reports whether the host sink also plays (--audio-dup); false
	// means the browser is the sole audio output.
	HostAudio bool `json:"hostAudio"`
}

func (c *webClient) sendHello() {
	dev := "device"
	if c.srv.sess != nil {
		dev = c.srv.sess.deviceName
	}
	c.cfgMu.Lock()
	raw := c.raw
	c.cfgMu.Unlock()
	c.writeJSON(webHello{
		Op:      "hello",
		Proto:   1,
		Device:  dev,
		Audio:   c.srv.audio != nil && c.srv.audio.err == nil,
		Raw:     raw,
		Version: version,
	})
}

func (c *webClient) sendStatus() {
	_, vw, vh := c.srv.stream.frames()
	c.mu.Lock()
	cv := c.cv
	c.mu.Unlock()
	gain := 100
	muted := false
	audio := "off"
	if c.srv.audio != nil {
		if c.srv.audio.err == nil {
			gain = c.srv.audio.gainPercent()
			muted = gain == 0
			audio = "on"
		} else {
			audio = "error"
		}
	}
	c.srv.clientsMu.Lock()
	n := int64(len(c.srv.clients))
	c.srv.clientsMu.Unlock()
	devVol, devIdx, devMax, devOK := c.srv.deviceVolume()
	st := webStatus{
		Op:        "status",
		FPS:       c.srv.stream.currentFPS,
		VideoW:    vw,
		VideoH:    vh,
		CanvasW:   cv.cw,
		CanvasH:   cv.ch,
		FitW:      cv.fw,
		FitH:      cv.fh,
		Gain:      gain,
		Muted:     muted,
		Audio:     audio,
		DevVol:    devVol,
		DevVolIdx: devIdx,
		DevVolMax: devMax,
		DevVolOK:  devOK,
		Clients:   n,
		Sent:      c.sentVideo.Load(),
		Control:   c.srv.ctrl != nil,
		Keys:      1,
		JPEGms:    c.srv.encodeMs(),
		AudioPeak: c.srv.stream.peakAudio(),
		HostAudio: c.srv.audio != nil && !c.srv.audio.silent.Load(),
	}
	c.writeJSON(st)
}

func (c *webClient) writeJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := c.ws.WriteText(string(b)); err != nil {
		c.closed.Store(true)
	}
}

// ---------------------------------------------------------------------------
// maintenance: encoder reaping + headless heartbeat
// ---------------------------------------------------------------------------

func (s *webServer) maintenance() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	var lastFrames int64
	lastAt := time.Now()
	for {
		select {
		case <-s.done:
			return
		case now := <-t.C:
			// Drop encoders nobody used for 30s (a device rotation leaves one
			// stale encoder per geometry). The write lock keeps this from
			// racing an encode that is already running.
			s.encUse.Lock()
			s.encMu.Lock()
			for k, e := range s.enc {
				if now.UnixNano()-e.lastUse > int64(30*time.Second) {
					e.h.free()
					delete(s.enc, k)
				}
			}
			s.encMu.Unlock()
			s.encUse.Unlock()
			frames := s.frames.Load()
			if s.headless {
				fps := float64(frames-lastFrames) / now.Sub(lastAt).Seconds()
				fmt.Fprintf(stderrWriter(), "scterm: web %d client(s) %.1f fps\n",
					s.nClients.Load(), fps)
			}
			lastFrames = frames
			lastAt = now
		}
	}
}

// ---------------------------------------------------------------------------
// encoder pool
// ---------------------------------------------------------------------------

// encoderFor returns a JPEG encoder pinned to the canvas geometry. The
// returned encoder must be used under a held s.encUse read lock (encodeWith
// does this); the caller must call releaseEnc when done.
func (s *webServer) encoderFor(cv webCanvas) *jpegEncoder {
	key := uint64(uint32(cv.cw))<<32 | uint64(uint32(cv.ch))
	s.encMu.Lock()
	defer s.encMu.Unlock()
	e := s.enc[key]
	if e == nil {
		h := jencOpen(cv.cw, cv.ch, s.quality())
		if h == nil {
			return nil
		}
		e = &jpegEncoder{h: h, w: cv.cw, hgt: cv.ch, maxSize: h.maxSize()}
		s.enc[key] = e
	}
	return e
}

// quality is the mjpeg qscale, clamped to a usable range.
func (s *webServer) quality() int {
	q := s.cfg.webQuality
	if q == 0 {
		q = webDefaultQuality
	}
	if q < webMinQuality {
		q = webMinQuality
	}
	if q > webMaxQuality {
		q = webMaxQuality
	}
	return q
}

// encodeWith runs one encode with the pool pinned, so concurrent shutdown or
// reaping cannot free the encoder mid-frame.
//
// The length check is not redundant with sendFrame's: the native mjpeg encoder
// reads exactly w*h*4 bytes from the pointer it is given and has no way to know
// how long that buffer is, so an undersized buffer is an out-of-bounds read
// (measured: megabytes past the end, SIGSEGV). This is the last gate before
// cgo, so it stays even though every current caller already validates.
func (s *webServer) encodeWith(cv webCanvas, bgr0, dst []byte, dstOff int) (int, error) {
	if need := cv.cw * cv.ch * 4; need <= 0 || len(bgr0) < need {
		return 0, fmt.Errorf("%w: have %d bytes, canvas %dx%d needs %d",
			errShortFrame, len(bgr0), cv.cw, cv.ch, need)
	}
	s.encUse.RLock()
	defer s.encUse.RUnlock()
	enc := s.encoderFor(cv)
	if enc == nil {
		return 0, errJencUnavailable
	}
	// One encode at a time per encoder: clients sharing a canvas share the
	// encoder, and an AVCodecContext cannot be used concurrently. Encoding the
	// same pixels once per client also means the shared-encoder path costs
	// clients x encode, so this is the hot path to optimise next (encode once
	// and share the payload between clients).
	enc.mu.Lock()
	// Time the native encode only, and only while holding the lock: timing
	// around encodeWith would also count the wait for this mutex, so with
	// several clients sharing one encoder jpegMs reported queue time (measured
	// 6.5 ms) instead of what the encoder actually costs (~2 ms).
	t0 := time.Now()
	n, err := enc.h.encodeInto(bgr0, dst, dstOff)
	s.noteEncodeTime(time.Since(t0))
	enc.mu.Unlock()
	return n, err
}

var errJencUnavailable = errors.New("jpeg encoder unavailable")

// errShortFrame means a frame buffer cannot fill the canvas it was paired
// with. It must be an error, never a best-effort encode: the native encoder
// would read past the buffer.
var errShortFrame = errors.New("frame buffer smaller than canvas")

// encoderMaxSize returns the buffer size needed for one frame at this
// geometry (0 when no encoder could be created).
func (s *webServer) encoderMaxSize(cv webCanvas) int {
	s.encUse.RLock()
	defer s.encUse.RUnlock()
	s.encMu.Lock()
	defer s.encMu.Unlock()
	key := uint64(uint32(cv.cw))<<32 | uint64(uint32(cv.ch))
	e := s.enc[key]
	if e == nil {
		h := jencOpen(cv.cw, cv.ch, s.quality())
		if h == nil {
			return 0
		}
		e = &jpegEncoder{h: h, w: cv.cw, hgt: cv.ch, maxSize: h.maxSize()}
		s.enc[key] = e
	}
	e.lastUse = time.Now().UnixNano()
	return e.maxSize
}

// freeEncoders tears the pool down. It waits for in-flight encodes to finish.
func (s *webServer) freeEncoders() {
	s.encUse.Lock()
	defer s.encUse.Unlock()
	s.encMu.Lock()
	defer s.encMu.Unlock()
	for _, e := range s.enc {
		e.h.free()
	}
	s.enc = make(map[uint64]*jpegEncoder)
}

// clientCount is the number of live clients (safe from any goroutine).
func (s *webServer) clientCount() int {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	return len(s.clients)
}

// logLimited rate-limits per-server error logging to at most one line per
// second, so a broken client cannot flood the terminal.
func (s *webServer) logLimited(format string, args ...any) {
	now := time.Now().UnixNano()
	if now-s.dropLog.Load() < int64(time.Second) {
		return
	}
	s.dropLog.Store(now)
	fmt.Fprintf(stderrWriter(), format, args...)
}
