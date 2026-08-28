// scterm 3.0 (Go) — Android remote control.
//
// Engine: scrcpy 4.1 spawns its hardware-encode server (H.264 + Opus) and
// records into a FIFO as live Matroska; ONE ffmpeg process decodes it and
// fans out three streams:
//
//   pipe:1  MJPEG  (pre-scaled to the TUI size, per fit mode) -> TUI + web fallback
//   pipe:3  fMP4   (H.264 copy, fragmented, 2s keyframes)     -> web MSE <video>
//   pipe:4  Ogg Opus (audio copy)                             -> web <audio>
//
// TUI: half-block mosaic, live-touch mouse drag, Zellij-aware.
// Web: MSE H264 player with MJPEG fallback, touch, audio, sliders.
// --window: pass-through to `scrcpy` itself.
//
// Fallback when scrcpy is missing: adb screenrecord -> ffmpeg (no audio).

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"scterm/engine"
)

const VERSION = "3.0.0-go"

// ---------------------------------------------------------------------------
// constants
// ---------------------------------------------------------------------------

var KEYCODE = map[string]int{
	"MENU": 82, "HOME": 3, "BACK": 4, "APP_SWITCH": 187, "POWER": 26,
	"VOL_UP": 24, "VOL_DOWN": 25, "CENTER": 23, "ENTER": 66, "TAB": 61,
	"DPAD_UP": 19, "DPAD_DOWN": 20, "DPAD_LEFT": 21, "DPAD_RIGHT": 22,
	"DEL": 67, "ESC": 111, "WAKEUP": 224,
}

var FIT_MODES = []string{"contain", "cover", "fill"}

const (
	C_ACCENT  = "\x1b[38;2;0;220;180m"
	C_DIM     = "\x1b[38;2;130;130;135m"
	C_TEXT    = "\x1b[38;2;225;225;230m"
	C_GREEN   = "\x1b[38;2;110;255;160m"
	C_YELLOW  = "\x1b[38;2;255;220;120m"
	C_MAGENTA = "\x1b[38;2;255;120;220m"
	C_BLUE    = "\x1b[38;2;120;180;255m"
	C_RED     = "\x1b[38;2;255;110;110m"
	RESET     = "\x1b[0m"
	BLOCK     = "▀"
)

// atomicWrite writes the whole buffer to fd, retrying short writes so
// Zellij never interleaves stray bytes between partial chunks.
func atomicWrite(fd int, data []byte) {
	for len(data) > 0 {
		n, err := syscall.Write(fd, data)
		if err != nil {
			return
		}
		data = data[n:]
	}
}

func dbg(format string, a ...interface{}) {
	if os.Getenv("SCTERM_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[scterm] "+format+"\n", a...)
	}
}

func logf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "[scterm] "+format+"\n", a...)
}

// ---------------------------------------------------------------------------
// adb helpers & input
// ---------------------------------------------------------------------------

// adbArgs builds the args AFTER "adb" (call sites do exec.Command("adb", args...)).
func adbArgs(serial string, tail ...string) []string {
	a := []string{}
	if serial != "" {
		a = append(a, "-s", serial)
	}
	return append(a, tail...)
}

func shellOut(serial string, tail ...string) string {
	// adb needs the `shell` subcommand: adb [-s SERIAL] shell <tail...>
	args := append(adbArgs(serial, "shell"), tail...)
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		dbg("shellOut %v err=%v out=%q", tail, err, truncate(out, 90))
		return ""
	}
	return string(out)
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return append(append([]byte(nil), b[:n]...), []byte("...")...)
	}
	return b
}

// AdbInput is one persistent `adb shell` pipe; fire-and-forget commands.
type AdbInput struct {
	serial string
	mu     sync.Mutex
	in     io.WriteCloser
	cmd    *exec.Cmd
}

func NewAdbInput(serial string) *AdbInput { return &AdbInput{serial: serial} }

func (a *AdbInput) ensure() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.in != nil && a.cmd != nil && a.cmd.ProcessState == nil {
		return true
	}
	// stale pipe from a dead adb shell — reconnect
	if a.cmd != nil && a.cmd.Process != nil {
		a.cmd.Process.Kill()
		a.cmd.Wait()
	}
	a.in = nil
	cmd := exec.Command("adb", adbArgs(a.serial, "shell")...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	a.cmd, a.in = cmd, in
	return true
}

func (a *AdbInput) send(cmd string) {
	if !a.ensure() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.in != nil {
		a.in.Write([]byte(cmd + "\n"))
	}
}

func (a *AdbInput) Tap(x, y int) { a.send(fmt.Sprintf("input tap %d %d", x, y)) }
func (a *AdbInput) Swipe(x1, y1, x2, y2, ms int) {
	a.send(fmt.Sprintf("input swipe %d %d %d %d %d", x1, y1, x2, y2, ms))
}
func (a *AdbInput) TouchDown(x, y int) { a.send(fmt.Sprintf("input motionevent DOWN %d %d", x, y)) }
func (a *AdbInput) TouchMove(x, y int) { a.send(fmt.Sprintf("input motionevent MOVE %d %d", x, y)) }
func (a *AdbInput) TouchUp(x, y int)   { a.send(fmt.Sprintf("input motionevent UP %d %d", x, y)) }
func (a *AdbInput) Key(code int)       { a.send(fmt.Sprintf("input keyevent %d", code)) }
func (a *AdbInput) Text(s string) {
	if s == "" {
		return
	}
	for len(s) > 180 {
		a.send("input text '" + strings.ReplaceAll(s[:180], "'", "'\\''") + "'")
		s = s[180:]
	}
	a.send("input text '" + strings.ReplaceAll(s, "'", "'\\''") + "'")
}

// Input abstracts the device-input sink (adb shell or engine control).
type Input interface {
	Tap(x, y int)
	Swipe(x1, y1, x2, y2, ms int)
	TouchDown(x, y int)
	TouchMove(x, y int)
	TouchUp(x, y int)
	Key(code int)
	Text(s string)
	Close()
}

// ProtoInput sends input over the engine's control socket (scrcpy wire
// protocol) instead of adb shell commands.
type ProtoInput struct {
	serial string
	ctrl   *engine.Control
	w, h   int // current device size for scroll/position math
}

func (p *ProtoInput) SetSize(w, h int) { p.w, p.h = w, h }

func (p *ProtoInput) Tap(x, y int)          { p.ctrl.Tap(x, y, max(p.w, 1), max(p.h, 1)) }
func (p *ProtoInput) TouchDown(x, y int)    { p.ctrl.Touch(0, x, y, max(p.w, 1), max(p.h, 1)) }
func (p *ProtoInput) TouchMove(x, y int)    { p.ctrl.Touch(2, x, y, max(p.w, 1), max(p.h, 1)) }
func (p *ProtoInput) TouchUp(x, y int)      { p.ctrl.Touch(1, x, y, max(p.w, 1), max(p.h, 1)) }
func (p *ProtoInput) Swipe(x1, y1, x2, y2, ms int) {
	p.ctrl.Touch(0, x1, y1, max(p.w, 1), max(p.h, 1))
	steps := 8
	for i := 1; i <= steps; i++ {
		time.Sleep(time.Duration(ms/steps) * time.Millisecond)
		p.ctrl.Touch(2, x1+(x2-x1)*i/steps, y1+(y2-y1)*i/steps, max(p.w, 1), max(p.h, 1))
	}
	p.ctrl.Touch(1, x2, y2, max(p.w, 1), max(p.h, 1))
}
func (p *ProtoInput) Scroll(x, y int, hScroll, vScroll float64) {
	p.ctrl.Scroll(x, y, max(p.w, 1), max(p.h, 1), hScroll, vScroll)
}
func (p *ProtoInput) Key(code int) {
	p.ctrl.Key(0, code)
	time.Sleep(30 * time.Millisecond)
	p.ctrl.Key(1, code)
}
func (p *ProtoInput) Text(s string) {
	// best-effort: ASCII via key codes would need a mapping; send the
	// clipboard route instead (set + paste) — engine control supports it
	p.ctrl.SetClipboard(s)
}
func (p *ProtoInput) Close() {}

// gamepadFeed pushes viewer gamepads to the device. Engine mode: a UHID
// gamepad (lazy create on first report). No-op without the engine.
type gamepadFeed struct {
	cap     *Capture
	created bool
}

func (g *gamepadFeed) feed(st engine.GamepadState) {
	s := g.cap.ses
	if s == nil || g.cap.protoIn == nil {
		return
	}
	if !g.created {
		s.Ctrl.UhidCreate(0x2000, 0x054c, 0x09cc, "scterm gamepad", engine.GamepadDesc)
		g.created = true
	}
	st.LX, st.LY = -st.LX, -st.LY
	s.Ctrl.UhidInput(0x2000, st.Report())
}

func (a *AdbInput) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		a.cmd.Process.Kill()
		a.cmd.Wait()
	}
	a.cmd, a.in = nil, nil
}

// ---------------------------------------------------------------------------
// device info
// ---------------------------------------------------------------------------

// deviceSize returns the CURRENT rotated display size — `wm size` gives the
// unrotated physical panel; the window manager's cur=WxH is the rotated one
// that screenrecord/screencap/scrcpy actually capture.
func deviceSize(serial string) (int, int) {
	out := shellOut(serial, "dumpsys", "window")
	if i := strings.Index(out, "cur="); i >= 0 {
		var w, h int
		if _, err := fmt.Sscanf(out[i+4:], "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	out = shellOut(serial, "wm", "size")
	var w, h int
	if _, err := fmt.Sscanf(out, "Physical size: %dx%d", &w, &h); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 1920, 1080
}

func modelName(serial string) string {
	if m := strings.TrimSpace(shellOut(serial, "getprop", "ro.product.model")); m != "" {
		return m
	}
	if serial != "" {
		return serial
	}
	return "?"
}

func batteryLevel(serial string) int {
	out := shellOut(serial, "dumpsys", "battery")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "level:") {
			if n, err := strconv.Atoi(strings.TrimSpace(line[6:])); err == nil {
				return n
			}
		}
	}
	return -1
}

func setRotation(serial string, deg int) {
	if serial == "" {
		return
	}
	shellOut(serial, "settings", "put", "system", "accelerometer_rotation", "0")
	shellOut(serial, "settings", "put", "system", "user_rotation", strconv.Itoa((deg/90)%4))
}

// hostOut runs a HOST-side adb command (e.g. `adb devices`) — no `shell`.
func hostOut(tail ...string) string {
	out, err := exec.Command("adb", tail...).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

func wakeUnlock(serial string, stayAwake bool) {
	if serial == "" {
		return
	}
	shellOut(serial, "input", "keyevent", fmt.Sprint(KEYCODE["WAKEUP"]))
	shellOut(serial, "wm", "dismiss-keyguard")
	if stayAwake {
		shellOut(serial, "svc", "power", "stayon", "true")
	}
}

// ---------------------------------------------------------------------------
// fit math (mirrors the ffmpeg -vf geometry for the TUI mjpeg output)
// ---------------------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mapToDevice converts 1-based canvas char coords to device pixel coords.
// Geometry mirrors Capture.mjpegVf.
func mapToDevice(dw, dh, mx, my, cols, rows int, mode string) (int, int) {
	rows2 := rows * 2
	var ox, oy, ow, oh int
	var fx, fy float64
	switch mode {
	case "fill":
		ow, oh = cols, rows2
		fx = (float64(mx) - 0.5) / float64(ow)
		fy = (float64(my*2) - 1.5) / float64(oh)
	case "cover":
		s := maxF(float64(cols)/float64(dw), float64(rows2)/float64(dh))
		ow = maxI(1, int(float64(dw)*s))
		oh = maxI(1, int(float64(dh)*s))
		ox = (ow - cols) / 2
		oy = (oh - rows2) / 2
		fx = (float64(mx) - 1 + float64(ox) + 0.5) / float64(ow)
		fy = (float64(my-1)*2 + float64(oy) + 0.5) / float64(oh)
	default: // contain
		s := minF(float64(cols)/float64(dw), float64(rows2)/float64(dh))
		ow = maxI(1, int(float64(dw)*s))
		oh = maxI(1, int(float64(dh)*s))
		ox = (cols - ow) / 2
		oy = (rows2 - oh) / 2
		fx = (float64(mx) - 1 - float64(ox) + 0.5) / float64(ow)
		fy = (float64(my-1)*2 - float64(oy) + 0.5) / float64(oh)
	}
	dx := int(fx*float64(dw) + 0.5)
	dy := int(fy*float64(dh) + 0.5)
	return clamp(dx, 0, dw-1), clamp(dy, 0, dh-1)
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// capture: scrcpy FIFO -> ffmpeg 3-way fan-out
// ---------------------------------------------------------------------------

// StreamSlot is a single-consumer byte channel (fmp4/ogg). New subscribers
// replace old ones; the pump always drains the source pipe so ffmpeg never
// blocks on a missing reader.
type StreamSlot struct {
	mu sync.Mutex
	ch chan []byte
}

func (s *StreamSlot) Sub() chan []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ch = make(chan []byte, 256)
	return s.ch
}

func (s *StreamSlot) Publish(b []byte) {
	s.mu.Lock()
	ch := s.ch
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- b:
	default:
	}
}

type Capture struct {
	serial  string
	engine  string      // "scrcpy" | "screenrecord" | "engine"
	useEngine bool     // use the native protocol engine
	ses     *engine.Session
	protoIn *ProtoInput
	maxSize int
	bitrate int
	jpegQ   int
	fpsCap  int
	fitMode string

	fifo string
	scr  *exec.Cmd
	ff   *exec.Cmd
	stop chan struct{}

	// mjpeg latest-frame cache (TUI-sized pipe:1)
	mu         sync.Mutex
	latest     []byte
	seq        uint64
	lastPub    time.Time
	streamLast time.Time // last STREAM (readMJPEG) publish, not filler
	firstPub   time.Time
	pubFPS     float64
	pubWin     []time.Time

	// web-sized mjpeg cache (pipe:5, 960x540)
	webMu  sync.Mutex
	webJpg []byte
	webSeq uint64
	webPub time.Time
	webWin []time.Time
	tuiWin []time.Time

	// fmp4/ogg fan-out
	fmp4     *StreamSlot
	ogg      *StreamSlot
	codec    string
	fmp4Init []byte
	oggInit  []byte

	termW, termH int

	engMu       sync.Mutex
	engineDead  bool
	stopping    bool

	infoMu  sync.Mutex
	model   string
	battery int

	pipesWg sync.WaitGroup
}

func NewCapture(serial string, maxSize, bitrate, jpegQ int) *Capture {
	c := &Capture{
		serial:  serial,
		maxSize: maxSize,
		bitrate: bitrate,
		jpegQ:   jpegQ,
		fpsCap:  30,
		fitMode: "contain",
		engine:  "scrcpy",
		stop:    make(chan struct{}),
		fmp4:    &StreamSlot{},
		ogg:     &StreamSlot{},
		model:   modelName(serial),
		battery: batteryLevel(serial),
	}
	return c
}

func (c *Capture) Start() {
	c.spawnPipeline()
	go c.infoLoop()
	go c.watchdog()
	go c.filler()
}

func (c *Capture) spawnPipeline() {
	c.teardown()
	c.fmp4 = &StreamSlot{}
	c.ogg = &StreamSlot{}
	c.fmp4Init = nil
	c.oggInit = nil
	c.codec = ""

	var ffArgs []string
	var scr *exec.Cmd
	var ses *engine.Session

	if c.useEngine {
		// NATIVE ENGINE: raw protocol session (no scrcpy, no mkv)
		var err error
		ms := c.maxSize
		if ms <= 0 {
			ms = 1280 // engine default ~720p (halves ffmpeg decode cost)
		}
		ses, err = engine.Open(c.serial, engine.Options{
			Audio:            true,
			MaxSize:          ms,
			CodecOptions:     "i-frame-interval=2",
			ClipboardAutosync: false,
		})
		if err != nil {
			logf("engine open failed: %v", err)
			return
		}
		// drain the socket backlog accrued during session setup so the
		// viewer starts at CURRENT content, not old frames
		ses.Video.DiscardUntilQuiet(250 * time.Millisecond)
		c.ses = ses
		c.engine = "engine"
		if c.protoIn != nil {
			c.protoIn.ctrl = ses.Ctrl
		}
		ffArgs = []string{"-hide_banner", "-loglevel", "error",
			"-flags", "low_delay", "-probesize", "32768", "-analyzeduration", "0",
			"-f", "h264", "-i", "pipe:0"}
	} else if hasScrcpy() {
		fifo := fmt.Sprintf("/tmp/scterm_go_%d.mkv", os.Getpid())
		os.Remove(fifo)
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			logf("mkfifo: %v", err)
			return
		}
		c.fifo = fifo

		args := []string{"-n", "-N", "--no-window"}
		args = append(args, fmt.Sprintf("--max-size=%d", c.maxSize))
		args = append(args, "--record="+fifo, "--record-format=mkv", "--verbosity=error")
		if c.serial != "" {
			args = append(args, "-s", c.serial)
		}
		if c.bitrate > 0 {
			args = append(args, fmt.Sprintf("--video-bit-rate=%d", c.bitrate))
		}
		args = append(args, "--max-fps=30")
		args = append(args, "--video-codec-options=i-frame-interval=2")
		// clear stale device-side servers from killed clients (they block
		// new scrcpy starts: 'server already running')
		exec.Command("adb", adbArgs(c.serial, "shell", "pkill -f scrcpy-server")...).Run()
		time.Sleep(200 * time.Millisecond)
		cmd := exec.Command("scrcpy", args...)
		cmd.Env = os.Environ()
		if headless() {
			cmd.Env = append(cmd.Env, "SDL_VIDEODRIVER=dummy", "XDG_RUNTIME_DIR="+os.TempDir())
		}
		// fully silent: no window, no console noise
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			logf("scrcpy failed: %v", err)
		} else {
			scr = cmd
			go func(cmd *exec.Cmd) {
				cmd.Wait()
				c.engMu.Lock()
				if !c.stopping {
					c.engineDead = true
				}
				c.engMu.Unlock()
			}(cmd)
		}
	}
	c.scr = scr

	if !c.useEngine {
		if scr != nil {
			c.engine = "scrcpy"
			ffArgs = []string{"-hide_banner", "-loglevel", "error",
				"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "32768",
				"-analyzeduration", "0", "-f", "matroska", "-i", c.fifo}
		} else {
			// fallback: adb screenrecord -> h264 pipe (no audio)
			c.engine = "screenrecord"
			ffArgs = []string{"-hide_banner", "-loglevel", "error",
				"-flags", "low_delay", "-probesize", "32768", "-r", "60",
				"-f", "h264", "-i", "pipe:0"}
		}
	}
	ffArgs = append(ffArgs,
		// pipe:1 — TUI-sized mjpeg, fit applied (tiny, instant decodes)
		"-map", "0:v", "-vf", c.mjpegVf(),
		"-c:v", "mjpeg", "-strict", "unofficial", "-q:v", fmt.Sprint(c.jpegQ),
		"-f", "image2pipe", "pipe:1",
		// pipe:3 — fMP4 for MSE (copy)
		"-map", "0:v", "-c:v", "copy",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:3",
		// pipe:5 — full-size mjpeg for the web viewer
		"-map", "0:v", "-vf", "scale=960:540",
		"-c:v", "mjpeg", "-strict", "unofficial", "-q:v", fmt.Sprint(c.jpegQ),
		"-f", "image2pipe", "pipe:5")
	if c.useEngine {
		// engine mode: no ogg output from ffmpeg — the engine's opus
		// packets are muxed to Ogg by us (zero ffmpeg in the audio path)
	} else {
		ffArgs = append(ffArgs,
			// pipe:4 — Ogg Opus audio (copy)
			"-map", "0:a?", "-c:a", "copy", "-f", "ogg", "pipe:4")
	}

	mjR, mjW, err := os.Pipe()
	if err != nil {
		logf("pipe: %v", err)
		return
	}
	f4R, f4W, err := os.Pipe()
	if err != nil {
		logf("pipe: %v", err)
		return
	}
	ogR, ogW, err := os.Pipe()
	if err != nil {
		logf("pipe: %v", err)
		return
	}
	webR, webW, err := os.Pipe()
	if err != nil {
		logf("pipe: %v", err)
		return
	}

	cmd := exec.Command("ffmpeg", ffArgs...)
	cmd.Stdout = mjW
	// stderr to devnull: ffmpeg's broken-pipe warnings on respawn were
	// landing inside the TUI render and corrupting the display
	cmd.Stderr = nil
	cmd.ExtraFiles = []*os.File{f4W, ogW, webW}
	var engStdin io.WriteCloser
	if c.useEngine {
		es, err := cmd.StdinPipe() // must happen BEFORE Start
		if err != nil {
			logf("ffmpeg stdin: %v", err)
			return
		}
		engStdin = es
	}
	if scr == nil && !c.useEngine {
		// wire adb screenrecord stdout as ffmpeg stdin
		r, w, err := os.Pipe()
		if err != nil {
			logf("pipe: %v", err)
			return
		}
		rec := exec.Command("adb", adbArgs(c.serial, "exec-out",
			"screenrecord --output-format=h264 --bit-rate "+strconv.Itoa(c.bitrate)+
				" --time-limit 179 -")...)
		rec.Stdout = w
		rec.Stderr = os.Stderr
		if err := rec.Start(); err != nil {
			logf("screenrecord failed: %v", err)
			return
		}
		w.Close()
		cmd.Stdin = r
		c.scr = rec
	}
	if err := cmd.Start(); err != nil {
		logf("ffmpeg failed: %v", err)
		return
	}
	go func(cmd *exec.Cmd) {
		cmd.Wait()
		c.engMu.Lock()
		if !c.stopping {
			c.engineDead = true
		}
		c.engMu.Unlock()
	}(cmd)
	mjW.Close()
	f4W.Close()
	if ses == nil {
		ogW.Close()
	}
	webW.Close()
	c.ff = cmd

	if ses != nil {
		// engine mode: feed raw H264 to ffmpeg; mux opus->Ogg ourselves
		stdin := engStdin
		go func() {
			for {
				pkt, err := ses.Video.Next()
				if err != nil {
					c.engMu.Lock()
					if !c.stopping {
						c.engineDead = true
					}
					c.engMu.Unlock()
					stdin.Close()
					return
				}
				if _, err := stdin.Write(pkt.Data); err != nil {
					return
				}
			}
		}()
		if ses.Audio != nil {
			go func() {
				mux := engine.NewOggMuxer(ogW, 2)
				if err := mux.WriteHeaders(); err != nil {
					return
				}
				for {
					pkt, err := ses.Audio.Next()
					if err != nil {
						ogW.Close()
						return
					}
					if err := mux.WritePacket(pkt); err != nil {
						return
					}
				}
			}()
		}
	}
	if c.protoIn != nil {
		// Touch coordinates must be in PHYSICAL device space (the viewers
		// normalize to deviceSize); the stream size (e.g. 720p via
		// max_size) is NOT the device size — using it clamps every
		// tap/drag to a wrong position.
		dw, dh := deviceSize(c.serial)
		if dw <= 0 || dh <= 0 {
			dw, dh = ses.Video.Width, ses.Video.Height
		}
		c.protoIn.SetSize(dw, dh)
	}

	c.pipesWg.Add(4)
	go c.readMJPEG(mjR)
	go c.readCopy(f4R, "fmp4")
	go c.readCopy(ogR, "ogg")
	go c.readWebJpeg(webR)
}

// readMJPEG splits the image2pipe JPEG stream into frames.
func (c *Capture) readMJPEG(r *os.File) {
	defer c.pipesWg.Done()
	defer r.Close()
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 65536)
	soi := []byte{0xff, 0xd8}
	eoi := []byte{0xff, 0xd9}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if os.Getenv("SCTERM_DEBUG") != "" && c.seq%20 == 0 {
				dbg("mjpeg reader: +%dB (seq=%d)", n, c.seq)
			}
		}
		if err != nil {
			dbg("mjpeg reader EOF: %v", err)
			return
		}
		for {
			s := indexOf(buf, soi)
			if s < 0 {
				break
			}
			e := indexOf(buf[s+2:], eoi)
			if e < 0 {
				break
			}
			e += s + 4
			frame := append([]byte(nil), buf[s:e]...)
			buf = buf[e:]
			c.publishLocked(frame, "pipe")
		}
		if len(buf) > 2<<20 {
			if i := indexOf(buf, soi); i >= 0 {
				buf = append([]byte(nil), buf[i:]...)
			} else {
				buf = buf[:0]
			}
		}
	}
}

func (c *Capture) publishLocked(frame []byte, srcName string) {
	// cap: skip if publishing faster than 30fps (the flood is from the
	// un-throttled mjpeg output; filler is ~2fps and fine)
	c.mu.Lock()
	last := c.lastPub
	c.mu.Unlock()
	if !last.IsZero() && time.Since(last) < 16*time.Millisecond && len(frame) > 1000 {
		return
	}
	if os.Getenv("SCTERM_DEBUG") != "" && c.seq%10 == 0 {
		dbg("publish seq=%d len=%d src=%s", c.seq, len(frame), srcName)
	}
	now := time.Now()
	c.mu.Lock()
	c.latest = frame
	c.seq++
	if c.firstPub.IsZero() {
		c.firstPub = now
	}
	// publish rate over the last 2s (robust to bursty simultaneous
	// publishers — an EMA on inter-arrival exploded to thousands)
	c.pubWin = append(c.pubWin, now)
	cut := now.Add(-2 * time.Second)
	i := 0
	for i < len(c.pubWin) && c.pubWin[i].Before(cut) {
		i++
	}
	c.pubWin = c.pubWin[i:]
	if n := len(c.pubWin); n > 0 {
		f := float64(n) / 2.0
		if f > 120 {
			f = 120
		}
		c.pubFPS = f
	}
	c.tuiWin = append(c.tuiWin, now)
	cut3 := now.Add(-2 * time.Second)
	for len(c.tuiWin) > 0 && c.tuiWin[0].Before(cut3) {
		c.tuiWin = c.tuiWin[1:]
	}
	c.lastPub = now
	if srcName == "pipe" {
		c.streamLast = now
	}
	c.mu.Unlock()
}

// readWebJpeg splits web-sized jpegs (960x540) into their own cache so
// the browser always sees a full-size frame, independent of the TUI size.
func (c *Capture) readWebJpeg(r *os.File) {
	defer c.pipesWg.Done()
	defer r.Close()
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 65536)
	soi := []byte{0xff, 0xd8}
	eoi := []byte{0xff, 0xd9}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return
		}
		for {
			s := indexOf(buf, soi)
			if s < 0 {
				break
			}
			e := indexOf(buf[s+2:], eoi)
			if e < 0 {
				break
			}
			e += s + 4
			frame := append([]byte(nil), buf[s:e]...)
			buf = buf[e:]
			now := time.Now()
			c.webMu.Lock()
			c.webJpg = frame
			c.webSeq++
			c.webPub = now
			c.webWin = append(c.webWin, now)
			cut2 := now.Add(-2 * time.Second)
			for len(c.webWin) > 0 && c.webWin[0].Before(cut2) {
				c.webWin = c.webWin[1:]
			}
			c.webMu.Unlock()
		}
		if len(buf) > 2<<20 {
			if i := indexOf(buf, soi); i >= 0 {
				buf = append([]byte(nil), buf[i:]...)
			} else {
				buf = buf[:0]
			}
		}
	}
}

// WebLatest returns the most recent web-sized jpeg.
func (c *Capture) WebLatest() ([]byte, uint64, bool) {
	c.webMu.Lock()
	defer c.webMu.Unlock()
	if c.webJpg == nil {
		return nil, 0, false
	}
	return c.webJpg, c.webSeq, true
}

// readCopy drains a copy pipe (fmp4/ogg) into its slot. The header bytes
// (moov/avcC for fmp4, OpusHead+OpusTags for ogg) are buffered, NOT
// published — every new subscriber gets them prepended by the endpoint.
func (c *Capture) readCopy(r *os.File, kind string) {
	defer c.pipesWg.Done()
	defer r.Close()
	tmp := make([]byte, 65536)
	scan := make([]byte, 0, 1<<20)
	publish := func(kind string, chunk []byte) {
		if kind == "fmp4" {
			c.fmp4.Publish(chunk)
		} else {
			c.ogg.Publish(chunk)
		}
	}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			chunk := append([]byte(nil), tmp[:n]...)
			if kind == "fmp4" && c.codec == "" {
				scan = append(scan, chunk...)
				if cc := scanAVCC(scan); cc != "" {
					c.codec = cc
					c.fmp4Init = append([]byte(nil), scan...)
				}
				continue // header bytes: buffered, not published
			}
			if kind == "ogg" && c.oggInit == nil {
				scan = append(scan, chunk...)
				if bytes.Count(scan, []byte("OggS")) >= 3 || len(scan) > 1<<20 {
					// init complete: headers are buffered; the page that
					// completed them is inside scan, so drop this chunk
					c.oggInit = append([]byte(nil), scan...)
					continue
				}
				continue
			}
			publish(kind, chunk)
		}
		if err != nil {
			return
		}
	}
}

// scanAVCC finds the avcC box in the init segment; returns "avc1.PPCCLL".
func scanAVCC(data []byte) string {
	i := bytes.Index(data, []byte("avcC"))
	if i < 0 || i+8 > len(data) {
		return ""
	}
	profile := data[i+5]
	compat := data[i+6]
	level := data[i+7]
	if profile == 0 && level == 0 {
		return ""
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", profile, compat, level)
}

func indexOf(h, n []byte) int {
	if len(n) == 0 {
		return 0
	}
	for i := 0; i+len(n) <= len(h); i++ {
		if bytes.Equal(h[i:i+len(n)], n) {
			return i
		}
	}
	return -1
}

// mjpegVf fits + scales the pipe:1 output to the TUI canvas exactly
// (contain pads, cover crops, fill stretches), so the TUI decodes a tiny
// image and renders it directly — no per-frame fit work, tiny writes.
func (c *Capture) mjpegVf() string {
	w, h := c.termW, c.termH
	if w <= 0 || h <= 0 {
		w, h = 100, 60
	}
	w, h = even(w), even(h)
	res := fmt.Sprintf("scale=%d:%d", w, h)
	switch c.fitMode {
	case "contain":
		return res + fmt.Sprintf(":force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=#080808", w, h)
	case "cover":
		return res + fmt.Sprintf(":force_original_aspect_ratio=increase,crop=%d:%d", w, h)
	}
	return res
}

func even(n int) int {
	if n < 2 {
		return 2
	}
	return n &^ 1
}

// SetFit applies a new fit mode (respawns ffmpeg geometry; rare).
func (c *Capture) SetFit(mode string) {
	if mode == c.fitMode {
		return
	}
	c.fitMode = mode
	c.spawnPipeline()
}

// ResizeTerm updates the TUI canvas size and respawns the pipeline so
// ffmpeg fits the pipe:1 output to the new geometry (regex: rare event).
func (c *Capture) ResizeTerm(w, h int) {
	if w == c.termW && h == c.termH {
		return
	}
	c.termW, c.termH = w, h
	c.spawnPipeline()
}

// RespawnFFMpeg kills and respawns ffmpeg (keeps scrcpy/fifo running).
func (c *Capture) RespawnFFMpeg() {
	if c.ff == nil {
		return
	}
	dbg("ffmpeg respawn (fit=%s term=%dx%d)", c.fitMode, c.termW, c.termH)
	c.spawnPipeline()
}

// Latest returns the most recent complete jpeg frame + seq.
func (c *Capture) Latest() ([]byte, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		return nil, 0, false
	}
	return c.latest, c.seq, true
}

func (c *Capture) Stats() (fps float64, model string, bat int) {
	c.mu.Lock()
	fps = c.pubFPS
	c.mu.Unlock()
	c.infoMu.Lock()
	model, bat = c.model, c.battery
	c.infoMu.Unlock()
	return
}

func (c *Capture) infoLoop() {
	for {
		select {
		case <-c.stop:
			return
		case <-time.After(5 * time.Second):
			if c.serial != "" {
				c.infoMu.Lock()
				if c.model == "?" || c.model == "" {
					c.model = modelName(c.serial)
				}
				c.battery = batteryLevel(c.serial)
				c.infoMu.Unlock()
			}
		}
	}
}

// watchdog restarts the pipeline if it goes silent for 8s.
func (c *Capture) watchdog() {
	lastRestart := time.Time{}
	for {
		select {
		case <-c.stop:
			return
		case <-time.After(2 * time.Second):
		}
		c.mu.Lock()
		seq := c.seq
		first := c.firstPub
		c.mu.Unlock()
		c.engMu.Lock()
		dead := c.engineDead
		c.engMu.Unlock()

		if dead && time.Since(lastRestart) > 3*time.Second {
			// an engine process actually crashed — respawn promptly
			logf("engine process died; respawning")
			lastRestart = time.Now()
			c.spawnPipeline()
			continue
		}
		// NOTE: a SILENT-but-alive stream is NOT a failure: this ROM
		// doesn't feed the encoder when the display is static (the
		// screencap filler keeps visuals fresh). Respawning every ~10s
		// on static screens flipped scrcpy's audio focus on the device
		// every cycle (user-observed on-device sound toggling).
		if seq == 0 && !first.IsZero() && time.Since(first) > 15*time.Second {
			logf("pipeline never produced frames; restarting")
			c.spawnPipeline()
			continue
		}
	}
}

// filler keeps fresh frames flowing while the engine stalls. On this
// ROM the display only feeds the encoder when content CHANGES, so a
// static screen yields ~1 frame even through scrcpy. When the stream has
// been silent >0.6s, the filler grabs a screencap (PNG via adb), fits it
// to the TUI geometry like ffmpeg does, JPEG-encodes it and publishes it.
func (c *Capture) filler() {
	defer func() {
		if r := recover(); r != nil {
			dbg("filler panic: %v", r)
		}
	}()
	for {
		select {
		case <-c.stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
		c.mu.Lock()
		// This ROM's encoder only emits on display damage, so a static
		// screen stalls the STREAM entirely (~1-2fps). The screencap
		// filler keeps the viewers alive at ~5fps on static screens —
		// slower than the old 3fps-only-when-stale (fresher, less laggy).
		stale := c.latest == nil || time.Since(c.lastPub) > 800*time.Millisecond
		c.mu.Unlock()
		if !stale || c.serial == "" {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		w, h := c.termW, c.termH
		if w <= 0 || h <= 0 {
			w, h = 640, 360
		}
		w, h = even(w), even(h)
		img := grabScreencap(c.serial)
		if img == nil {
			dbg("filler: screencap failed")
			time.Sleep(400 * time.Millisecond)
			continue
		}
		canvas := fitImage(img, w, h, c.fitMode)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 78}); err != nil {
			dbg("filler: jpeg encode failed: %v", err)
			continue
		}
		c.publishLocked(buf.Bytes(), "fill")
		// also keep the web cache alive on static screens
		wimg := fitImage(img, 960, 540, "contain")
		var wbuf bytes.Buffer
		if err := jpeg.Encode(&wbuf, wimg, &jpeg.Options{Quality: 70}); err == nil {
			c.webMu.Lock()
			c.webJpg = wbuf.Bytes()
			c.webSeq++
			c.webPub = time.Now()
			c.webMu.Unlock()
		}
		dbg("filler: published %d bytes", buf.Len())
	}
}

// grabScreencap pulls a PNG screencap from the device (self-sized).
func grabScreencap(serial string) image.Image {
	cmd := exec.Command("adb", adbArgs(serial, "exec-out", "screencap", "-p")...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		dbg("screencap err=%v len=%d msg=%q", err, len(out), errb.String())
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		return nil
	}
	return img
}

// fitImage fits src into w x h per mode, mirroring Capture.mjpegVf.
func fitImage(src image.Image, w, h int, mode string) image.Image {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	var ox, oy, ow, oh int
	switch mode {
	case "fill":
		ow, oh = w, h
	case "cover":
		s := maxF(float64(w)/float64(sw), float64(h)/float64(sh))
		ow = maxI(1, int(float64(sw)*s))
		oh = maxI(1, int(float64(sh)*s))
		ox = (ow - w) / 2
		oy = (oh - h) / 2
	default: // contain
		s := minF(float64(w)/float64(sw), float64(h)/float64(sh))
		ow = maxI(1, int(float64(sw)*s))
		oh = maxI(1, int(float64(sh)*s))
		ox = (w - ow) / 2
		oy = (h - oh) / 2
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	// dark background
	bg := color.RGBA{8, 8, 8, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			canvas.SetRGBA(x, y, bg)
		}
	}
	// nearest-neighbour sampling into the fitted rect
	for y := 0; y < oh; y++ {
		sy := (y * sh) / oh
		if sy >= sh {
			sy = sh - 1
		}
		for x := 0; x < ow; x++ {
			sxx := (x * sw) / ow
			if sxx >= sw {
				sxx = sw - 1
			}
			r, g, b, a := src.At(sb.Min.X+sxx, sb.Min.Y+sy).RGBA()
			canvas.SetRGBA(x+ox, y+oy, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)})
		}
	}
	return canvas
}

func (c *Capture) teardown() {
	c.engMu.Lock()
	c.stopping = true
	c.engineDead = false
	c.engMu.Unlock()
	for _, p := range []*exec.Cmd{c.ff, c.scr} {
		if p != nil && p.Process != nil {
			p.Process.Kill() // reaped by the launch goroutine
		}
	}
	c.ff, c.scr = nil, nil
	if c.ses != nil {
		c.ses.Close()
		c.ses = nil
	}
	if c.fifo != "" {
		os.Remove(c.fifo)
		c.fifo = ""
	}
}

func (c *Capture) Close() {
	c.teardown()
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	c.pipesWg.Wait()
}

func hasScrcpy() bool {
	_, err := exec.LookPath("scrcpy")
	return err == nil
}

func headless() bool {
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// half-block renderer
// ---------------------------------------------------------------------------

type Renderer struct {
	cols, rows int
	prev       []string
}

func (r *Renderer) Reset() { r.prev = nil }

// Render converts an image (already scaled to cols x rows*2) into RLE rows.
func (r *Renderer) Render(img image.Image, cols, rows int) []string {
	b := img.Bounds()
	out := make([]string, rows)
	// fast path: YCbCr planes
	if yc, ok := img.(*image.YCbCr); ok {
		ys := yc.Y
		ysStride := yc.YStride
		cb := yc.Cb
		cr := yc.Cr
		cs := yc.CStride
		sw, sh := b.Dx(), b.Dy()
		var top, bot [3]uint8
		for y := 0; y < rows; y++ {
			var sb strings.Builder
			sb.Grow(cols * 10)
			prevKey := ""
			run := 0
			emit := func() {
				if run > 0 {
					sb.WriteString(prevKey)
					for i := 0; i < run; i++ {
						sb.WriteString(BLOCK)
					}
					run = 0
				}
			}
			for x := 0; x < cols; x++ {
				// proportional + clamped sampling: any source size is safe
				// (a stale pre-resize frame must never panic the renderer)
				sx := b.Min.X + (x*sw)/cols
				if sx >= b.Max.X {
					sx = b.Max.X - 1
				}
				y0 := b.Min.Y + (y*2*sh)/(rows*2)
				y1 := b.Min.Y + ((y*2+1)*sh)/(rows*2)
				if y0 >= b.Max.Y {
					y0 = b.Max.Y - 1
				}
				if y1 >= b.Max.Y {
					y1 = b.Max.Y - 1
				}
				top = ycbcrAt(ys, ysStride, cb, cr, cs, sx, y0, b.Min)
				bot = ycbcrAt(ys, ysStride, cb, cr, cs, sx, y1, b.Min)
				k := escKey(top, bot)
				if k == prevKey {
					run++
				} else {
					emit()
					prevKey = k
					run = 1
				}
			}
			emit()
			out[y] = sb.String()
		}
		return out
	}
	// generic path
	for y := 0; y < rows; y++ {
		var sb strings.Builder
		sb.Grow(cols * 10)
		prevKey := ""
		run := 0
		emit := func() {
			if run > 0 {
				sb.WriteString(prevKey)
				for i := 0; i < run; i++ {
					sb.WriteString(BLOCK)
				}
				run = 0
			}
		}
		sw, sh := b.Dx(), b.Dy()
		for x := 0; x < cols; x++ {
			sx := b.Min.X + (x*sw)/cols
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			y0 := b.Min.Y + (y*2*sh)/(rows*2)
			y1 := b.Min.Y + ((y*2+1)*sh)/(rows*2)
			if y0 >= b.Max.Y {
				y0 = b.Max.Y - 1
			}
			if y1 >= b.Max.Y {
				y1 = b.Max.Y - 1
			}
			tr, tg, tb, _ := img.At(sx, y0).RGBA()
			br, bg, bb, _ := img.At(sx, y1).RGBA()
			k := escKey([3]uint8{uint8(tr >> 8), uint8(tg >> 8), uint8(tb >> 8)},
				[3]uint8{uint8(br >> 8), uint8(bg >> 8), uint8(bb >> 8)})
			if k == prevKey {
				run++
			} else {
				emit()
				prevKey = k
				run = 1
			}
		}
		emit()
		out[y] = sb.String()
	}
	return out
}

func ycbcrAt(ys []uint8, yStride int, cb, cr []uint8, cStride int, x, y int, min image.Point) [3]uint8 {
	yi := (y-min.Y)*yStride + (x - min.X)
	ci := (y-min.Y)/2*cStride + (x-min.X)/2
	r, g, b := color.YCbCrToRGB(ys[yi], cb[ci], cr[ci])
	return [3]uint8{r, g, b}
}

func escKey(top, bot [3]uint8) string {
	// quantize to 4 bits/channel: drops JPEG noise so adjacent cells
	// merge into long RLE runs. Full-redraw frames drop from ~80KB to
	// ~5-10KB — without this the always-full-redraw TUI writes 2.4MB/s
	// and backpressures on the pty, falling seconds behind.
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm",
		top[0]&0xE0, top[1]&0xE0, top[2]&0xE0,
		bot[0]&0xE0, bot[1]&0xE0, bot[2]&0xE0)
}

// ---------------------------------------------------------------------------
// terminal
// ---------------------------------------------------------------------------

func getTermSize() (int, int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

func setRaw(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return
	}
	t.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Iflag &^= unix.IXON | unix.ICRNL | unix.INLCR | unix.BRKINT | unix.ISTRIP
	t.Oflag &^= unix.OPOST
	unix.IoctlSetTermios(fd, unix.TCSETS, t)
}

func restoreTerm(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return
	}
	t.Lflag |= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Iflag |= unix.IXON | unix.ICRNL | unix.INLCR
	t.Oflag |= unix.OPOST
	unix.IoctlSetTermios(fd, unix.TCSETS, t)
}

// ---------------------------------------------------------------------------
// TUI
// ---------------------------------------------------------------------------

type TUI struct {
	cap      *Capture
	inp      Input
	fit      string
	chrome   bool
	grab     bool
	inZellij bool

	cols, lines int
	rows        int

	prevRows []string
	prevTop  string
	prevBot  string

	renderer Renderer

	textBuf   []rune
	menuOpen  bool
	menuIdx   int
	helpUntil time.Time
	quitUntil time.Time

	pressX, pressY       int
	pressed              bool
	dragged              bool
	lastTouch            time.Time
	lastMoveX, lastMoveY int

	dotX, dotY int
	dotUntil   time.Time

	exitNow    func()
	lastFrameT time.Time
	frameCount int
	resizeAt   time.Time
	resizeW    int
	resizeH    int

	started  time.Time
	frames   int
	lastDraw time.Time
	fpsEMA   float64
	spark    []float64

	devW, devH int
}

func NewTUI(c *Capture, inp Input, fit string, chrome bool) *TUI {
	t := &TUI{
		cap: c, inp: inp, fit: fit, chrome: chrome,
		inZellij: os.Getenv("ZELLIJ") != "",
		started:  time.Now(),
	}
	t.devW, t.devH = deviceSize(c.serial)
	return t
}

// ASCII-safe sparkline (the unicode ▁▂▃▄▅▆▇█ blocks aren't rendered
// in many Zellij/terminal font combos — showed as 'a' mojibake).
const SPARK = " .:-=+*#%@"

func (t *TUI) style(s string, color string) string {
	if color == "" {
		return s
	}
	return color + s + RESET
}

// draw renders one frame: chrome bars + canvas rows (incremental).
func (t *TUI) draw(rows []string, ms float64, capMode string) {
	cols, lines := t.cols, t.lines
	topRows := 0
	if t.chrome {
		topRows = 1
	}
	canvasRows := lines - 1 - topRows

	var out strings.Builder
	out.Grow(1 << 20)

	// HYBRID redraw: only changed rows (a full redraw per frame is
	// ~50KB and stalls the pty ~1s during motion), with a periodic
	// full repaint to scrub any stray leaked bytes.
	t.frameCount++
	changed := []int{}
	for i, s := range rows {
		if i >= len(t.prevRows) || t.prevRows[i] != s {
			changed = append(changed, i)
		}
	}
	full := len(t.prevRows) != len(rows) ||
		len(changed) > len(rows)/2 ||
		t.frameCount%24 == 0
	if full {
		fmt.Fprintf(&out, "\x1b[%d;1H", 1+topRows)
		for _, s := range rows {
			out.WriteString("\x1b[2K")
			out.WriteString(s)
			out.WriteString("\r\n")
		}
	} else {
		for _, i := range changed {
			fmt.Fprintf(&out, "\x1b[%d;1H\x1b[2K%s", i+1+topRows, rows[i])
		}
	}
	t.prevRows = append([]string(nil), rows...)

	if t.chrome {
		top := t.topBar(cols, canvasRows, ms, capMode)
		out.WriteString("\x1b[1;1H\x1b[2K")
		out.WriteString(top)
	}
	bot := t.bottomBar(cols)
	fmt.Fprintf(&out, "\x1b[%d;1H\x1b[2K%s", lines, bot)

	// cursor dot
	if t.dotUntil.After(time.Now()) {
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[38;5;45m●\x1b[0m",
			t.dotY+topRows, t.dotX)
	}
	// overlays
	if t.menuOpen {
		out.WriteString(t.menuOverlay(cols, lines))
	} else if t.helpUntil.After(time.Now()) {
		out.WriteString(t.helpOverlay(cols, lines))
	}
	if out.Len() > 0 {
		atomicWrite(int(os.Stdout.Fd()), []byte(out.String()))
	}
}

// chip renders one styled segment (python-app look: colored fg/bg).
func chip(s, fg, bg string) string {
	var sb strings.Builder
	if bg != "" {
		sb.WriteString("\x1b[48;2;" + bg + "m")
	}
	if fg != "" {
		sb.WriteString("\x1b[38;2;" + fg + "m")
	}
	sb.WriteString(s)
	sb.WriteString(RESET)
	return sb.String()
}

func (t *TUI) topBar(cols, canvasRows int, ms float64, capMode string) string {
	fps, model, bat := t.cap.Stats()
	var sb strings.Builder
	sb.WriteString(chip(" "+model+" ", "0;0;0", "0;220;180"))
	if t.cap.serial != "" {
		sb.WriteString(" " + chip(t.cap.serial, "120;120;125", ""))
	}
	sb.WriteString(" " + chip(fmt.Sprintf("%d×%d", t.devW, t.devH), "225;225;230", ""))
	modeCol := "110;255;160"
	if capMode == "screenrecord" {
		modeCol = "255;120;220"
	}
	sb.WriteString(" " + chip(" "+capMode+" ", "8;8;10", modeCol))
	sb.WriteString(" " + chip(fmt.Sprintf("%5.1f fps", fps), "255;220;120", ""))
	sb.WriteString(" " + chip(fmt.Sprintf("%4.1f ms", ms), "130;130;135", ""))
	sb.WriteString(" " + chip("fit "+t.fit, "120;180;255", ""))
	sb.WriteString(" " + chip(fmt.Sprintf("%dx%d", cols, canvasRows), "255;120;220", ""))
	if bat >= 0 {
		bcol := "110;255;160"
		if bat < 20 {
			bcol = "255;110;110"
		}
		sb.WriteString(" " + chip(fmt.Sprintf("bat %d%%", bat), bcol, ""))
	}
	s := sb.String()
	if len([]rune(s)) > cols {
		s = string([]rune(s)[:cols])
	}
	return s
}

func (t *TUI) bottomBar(cols int) string {
	var sb strings.Builder
	sb.WriteString(C_DIM)
	if t.inZellij {
		sb.WriteString(" F12=grab (mouse+keys)  Click=tap  Drag=touch  Wheel=scroll  ^T=menu  Esc×2=quit ")
	} else {
		sb.WriteString(" Click=tap  Drag=touch  Wheel=scroll  Type=text  ?=help  ^T=menu  F12=grab  Esc×2=quit ")
	}
	if len(t.spark) > 0 {
		sb.WriteString(" ")
		for _, v := range t.spark {
			idx := int(v * 7)
			if idx < 0 {
				idx = 0
			}
			if idx > 7 {
				idx = 7
			}
			sb.WriteString(string(SPARK[idx]))
		}
	}
	s := sb.String() + RESET
	if len([]rune(s)) > cols {
		s = string([]rune(s)[:cols])
	}
	return s
}

func (t *TUI) helpOverlay(cols, lines int) string {
	help := []string{
		C_ACCENT + " ── scterm " + VERSION + " ──────────────────────" + RESET,
		" Click / drag / wheel .. tap / live touch / scroll",
		" Typing + Enter ........ text input",
		" F1..F12 ............... menu home back recents power vol dpad",
		" ^T menu · ^F fit · ^S stream toggle",
		" F12 / Ctrl-Alt-G ...... grab mode (keys to device)",
		" Esc ×2 ................ quit",
	}
	return overlay(t, cols, lines, help)
}

func (t *TUI) menuOverlay(cols, lines int) string {
	items := []string{
		fmt.Sprintf("Fit mode ............ %s", t.fit),
		fmt.Sprintf("TUI fps ............. %d", t.cap.fpsCap),
		fmt.Sprintf("Engine .............. %s", t.cap.engine),
	}
	lines2 := []string{
		C_ACCENT + " ── menu (arrows, enter, esc) ──" + RESET,
	}
	for i, it := range items {
		if i == t.menuIdx {
			lines2 = append(lines2, C_YELLOW+" ▶ "+it+RESET)
		} else {
			lines2 = append(lines2, "   "+it)
		}
	}
	return overlay(t, cols, lines, lines2)
}

func overlay(t *TUI, cols, lines int, content []string) string {
	var sb strings.Builder
	w := 46
	h := len(content)
	x0 := (cols - w) / 2
	if x0 < 1 {
		x0 = 1
	}
	y0 := (lines - h) / 2
	if y0 < 1 {
		y0 = 1
	}
	for i, ln := range content {
		fmt.Fprintf(&sb, "\x1b[%d;%dH\x1b[48;2;30;30;34m%s\x1b[0m",
			y0+i, x0, ln)
	}
	return sb.String()
}

// ---- input ------------------------------------------------------------

type evKind int

const (
	evNone evKind = iota
	evChar
	evQuit
	evEsc
	evMenu
	evFit
	evStream
	evGrab
	evEnter
	evBack
	evFKey
	evDpad
	evMouse
	evHelp
)

type event struct {
	kind   evKind
	r      rune
	code   int // keycode
	btn    int // mouse button
	x, y   int // mouse cell (1-based)
	press  bool
	motion bool
	wheel  bool
}

// feed parses a chunk of raw terminal bytes into events.
func (t *TUI) feed(data []byte, evs *[]event) {
	i := 0
	for i < len(data) {
		b := data[i]
		if b == 0x1b {
			// escape sequence
			if i+1 >= len(data) {
				*evs = append(*evs, event{kind: evEsc})
				return
			}
			nxt := data[i+1]
			if nxt == '[' {
				// find terminator
				j := i + 2
				for j < len(data) && !(data[j] >= 0x40 && data[j] <= 0x7e) {
					j++
				}
				if j >= len(data) {
					return
				}
				seq := string(data[i+2 : j])
				fin := data[j]
				i = j + 1
				switch fin {
				case 'A':
					*evs = append(*evs, event{kind: evDpad, code: KEYCODE["DPAD_UP"]})
				case 'B':
					*evs = append(*evs, event{kind: evDpad, code: KEYCODE["DPAD_DOWN"]})
				case 'C':
					*evs = append(*evs, event{kind: evDpad, code: KEYCODE["DPAD_RIGHT"]})
				case 'D':
					*evs = append(*evs, event{kind: evDpad, code: KEYCODE["DPAD_LEFT"]})
				case 'H':
					*evs = append(*evs, event{kind: evDpad, code: KEYCODE["HOME"]})
				case 'M', 'm': // SGR mouse: <b;x;y  (b holds button+flags)
					parts := strings.Split(seq, ";")
					if len(parts) == 3 {
						btnRaw, _ := strconv.Atoi(strings.TrimPrefix(parts[0], "<"))
						x, _ := strconv.Atoi(parts[1])
						y, _ := strconv.Atoi(parts[2])
						press := fin == 'M'
						motion := btnRaw&32 != 0
						wheel := btnRaw&64 != 0
						*evs = append(*evs, event{kind: evMouse, btn: btnRaw,
							x: x, y: y, press: press, motion: motion, wheel: wheel})
					}
				default:
					// F-keys: ESC [ 1 1 ~ — the '~' is the terminator (fin),
					// so seq holds the code digits
					if fin == '~' {
						n, _ := strconv.Atoi(seq)
						*evs = append(*evs, event{kind: evFKey, code: n})
					} else {
						*evs = append(*evs, event{kind: evEsc})
					}
				}
			} else if nxt == 'O' {
				if i+2 < len(data) {
					switch data[i+2] {
					case 'P':
						*evs = append(*evs, event{kind: evFKey, code: 1})
					case 'Q':
						*evs = append(*evs, event{kind: evFKey, code: 2})
					case 'R':
						*evs = append(*evs, event{kind: evFKey, code: 3})
					case 'S':
						*evs = append(*evs, event{kind: evFKey, code: 4})
					}
					i += 3
					continue
				}
				i += 2
			} else if nxt == 0x1b {
				*evs = append(*evs, event{kind: evEsc})
				i += 2
			} else {
				*evs = append(*evs, event{kind: evEsc})
				i += 2
			}
			continue
		}
		// control chars
		if b == 0x0d || b == 0x0a {
			*evs = append(*evs, event{kind: evEnter})
			i++
			continue
		}
		if b == 0x7f || b == 0x08 {
			*evs = append(*evs, event{kind: evBack})
			i++
			continue
		}
		if b == 0x03 { // ^C
			*evs = append(*evs, event{kind: evQuit})
			i++
			continue
		}
		if b == 0x14 { // ^T
			*evs = append(*evs, event{kind: evMenu})
			i++
			continue
		}
		if b == 0x06 { // ^F
			*evs = append(*evs, event{kind: evFit})
			i++
			continue
		}
		if b == 0x13 { // ^S
			*evs = append(*evs, event{kind: evStream})
			i++
			continue
		}
		if b == 0x07 { // ^G — backup grab toggle (some terminals eat F12)
			*evs = append(*evs, event{kind: evGrab})
			i++
			continue
		}
		if b < 0x20 {
			i++
			continue
		}
		r, sz := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && sz <= 1 {
			i++
			continue
		}
		*evs = append(*evs, event{kind: evChar, r: r})
		i += sz
	}
}

// handle processes one parsed event.
func (t *TUI) handle(ev event) {
	switch ev.kind {
	case evQuit:
		t.quit()
	case evEsc:
		if t.menuOpen {
			t.menuOpen = false
		} else if time.Now().Before(t.quitUntil) {
			t.quit()
		} else {
			t.quitUntil = time.Now().Add(3 * time.Second)
		}
	case evMenu:
		t.menuOpen = !t.menuOpen
		t.menuIdx = 0
	case evFit:
		for i, m := range FIT_MODES {
			if m == t.fit {
				t.fit = FIT_MODES[(i+1)%len(FIT_MODES)]
				break
			}
		}
		t.cap.SetFit(t.fit)
		t.prevRows = nil
	case evStream:
		// scrcpy -> screenrecord fallback toggle
		if t.cap.engine == "scrcpy" {
			t.cap.engine = "screenrecord"
		} else {
			t.cap.engine = "scrcpy"
		}
		t.cap.spawnPipeline()
		t.prevRows = nil
	case evGrab:
		t.toggleGrab()
	case evChar:
		if ev.r == '?' {
			if time.Now().Before(t.helpUntil) {
				t.helpUntil = time.Time{}
			} else {
				t.helpUntil = time.Now().Add(4 * time.Second)
			}
			t.quitUntil = time.Time{}
			return
		}
		if t.menuOpen {
			return
		}
		t.textBuf = append(t.textBuf, ev.r)
	case evEnter:
		if t.menuOpen {
			t.menuSelect()
			return
		}
		if len(t.textBuf) > 0 {
			t.inp.Text(string(t.textBuf))
			t.textBuf = t.textBuf[:0]
		} else {
			t.inp.Key(KEYCODE["ENTER"])
		}
	case evBack:
		if len(t.textBuf) > 0 {
			t.textBuf = t.textBuf[:len(t.textBuf)-1]
		} else {
			t.inp.Key(KEYCODE["DEL"])
		}
	case evFKey:
		t.fkey(ev.code)
	case evDpad:
		if t.menuOpen {
			switch ev.code {
			case KEYCODE["DPAD_UP"]:
				t.menuIdx = (t.menuIdx + 2) % 3
			case KEYCODE["DPAD_DOWN"]:
				t.menuIdx = (t.menuIdx + 1) % 3
			}
			return
		}
		if len(t.textBuf) > 0 {
			t.inp.Text(string(t.textBuf))
			t.textBuf = t.textBuf[:0]
		}
		t.inp.Key(ev.code)
	case evMouse:
		t.mouse(ev)
	}
}

func (t *TUI) fkey(code int) {
	// CSI F-keys: F1..F5 = 11..15, F6..F10 = 17..21, F11=23, F12=24
	codes := map[int]string{11: "MENU", 12: "HOME", 13: "BACK", 14: "APP_SWITCH",
		15: "POWER", 17: "VOL_UP", 18: "VOL_DOWN", 19: "CENTER",
		20: "DPAD_UP", 21: "DPAD_DOWN", 23: "DPAD_LEFT", 24: "GRAB"}
	if name, ok := codes[code]; ok {
		if name == "GRAB" {
			t.toggleGrab()
			return
		}
		t.inp.Key(KEYCODE[name])
	}
}

func (t *TUI) toggleGrab() {
	t.grab = !t.grab
	if t.inZellij {
		mode := "normal"
		if t.grab {
			mode = "locked"
		}
		exec.Command("zellij", "action", "switch-mode", mode).Run()
	}
	// mouse reporting on/off
	if t.grab || !t.inZellij {
		os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	} else {
		os.Stdout.WriteString("\x1b[?1000l\x1b[?1002l\x1b[?1006l")
	}
	os.Stdout.Sync()
}

func (t *TUI) menuSelect() {
	switch t.menuIdx {
	case 0: // fit
		for i, m := range FIT_MODES {
			if m == t.fit {
				t.fit = FIT_MODES[(i+1)%len(FIT_MODES)]
				break
			}
		}
		t.cap.SetFit(t.fit)
		t.prevRows = nil
	case 1: // fps
		fps := t.cap.fpsCap
		fps = []int{8, 12, 20, 30, 60}[(fps/10+2)%5]
		if fps == 8 {
			fps = 8
		}
		t.cap.fpsCap = fps
	case 2: // engine
		if t.cap.engine == "scrcpy" {
			t.cap.engine = "screenrecord"
		} else {
			t.cap.engine = "scrcpy"
		}
		t.cap.spawnPipeline()
		t.prevRows = nil
	}
	t.menuOpen = false
}

func (t *TUI) mouse(ev event) {
	if t.menuOpen || t.helpUntil.After(time.Now()) {
		return
	}
	dw, dh := t.devW, t.devH
	topRows := 0
	if t.chrome {
		topRows = 1
	}
	cy := ev.y - topRows
	if cy < 1 || cy > t.rows {
		return
	}
	cols := t.cols
	btn := ev.btn & 3
	if ev.wheel {
		cx := dw / 2
		// SGR wheel: raw button ends in 68 (up) / 69 (down)
		if ev.btn&1 == 0 {
			t.inp.Swipe(cx, int(float64(dh)*0.35), cx, int(float64(dh)*0.65), 90)
		} else {
			t.inp.Swipe(cx, int(float64(dh)*0.65), cx, int(float64(dh)*0.35), 90)
		}
		return
	}
	if btn == 0 && ev.press {
		x, y := mapToDevice(dw, dh, ev.x, cy, cols, t.rows, t.fit)
		t.inp.TouchDown(x, y)
		t.pressed = true
		t.dragged = false
		t.pressX, t.pressY = ev.x, cy
		t.lastMoveX, t.lastMoveY = x, y
		t.lastTouch = time.Now()
		t.dotX, t.dotY = ev.x, cy
		t.dotUntil = time.Now().Add(4 * time.Second)
	} else if ev.motion && btn == 0 && t.pressed {
		x, y := mapToDevice(dw, dh, ev.x, cy, cols, t.rows, t.fit)
		t.lastMoveX, t.lastMoveY = x, y
		t.dragged = true
		t.dotX, t.dotY = ev.x, cy
		t.dotUntil = time.Now().Add(4 * time.Second)
		if time.Since(t.lastTouch) >= 60*time.Millisecond {
			t.inp.TouchMove(x, y)
			t.lastTouch = time.Now()
		}
	} else if btn == 0 && !ev.press && t.pressed {
		t.inp.TouchUp(t.lastMoveX, t.lastMoveY)
		t.pressed = false
		t.dragged = false
		t.dotUntil = time.Time{}
	}
}

func (t *TUI) quit() {
	t.exitNow()
}

// exitNow is set by Run; see Run.

// Run is the main TUI loop.
func (t *TUI) Run() {
	// alt-screen + hide cursor. Mouse ON by default OUTSIDE Zellij
	// (no PTY leak there — click/tap works immediately); in Zellij it
	// stays OFF until F12/Ctrl-G grab locks the pane (byte leak).
	if t.inZellij {
		os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000l\x1b[?1002l\x1b[?1006l")
	} else {
		os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	}
	os.Stdout.Sync()
	fd := int(os.Stdin.Fd())
	setRaw(fd)

	quit := make(chan struct{})
	stop := false

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigCh
		stop = true
		quit <- struct{}{}
	}()

	// stdin reader goroutine; EOF quits gracefully (pipes/script runs)
	stdinCh := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				buf2 := append([]byte(nil), buf[:n]...)
				stdinCh <- buf2
			}
			if err != nil {
				select {
				case quit <- struct{}{}:
				default:
				}
				return
			}
		}
	}()

	interval := time.Second / time.Duration(t.cap.fpsCap)
	lastDraw := time.Now()
	var frameSeq uint64

	t.exitNow = func() {
		stop = true
		select {
		case quit <- struct{}{}:
		default:
		}
	}

	for !stop {
		select {
		case data := <-stdinCh:
			var evs []event
			t.feed(data, &evs)
			for _, ev := range evs {
				t.handle(ev)
			}
		case <-time.After(interval / 2):
			// check resize + frame budget
		case <-quit:
			stop = true
		}
		// resize check, DEBOUNCED: record the pending size, apply only
		// after it stays stable ~400ms (window drags fire many SIGWINCHes
		// and each apply respawns the pipeline)
		cols, lines := getTermSize()
		if cols != t.cols || lines != t.lines {
			t.resizeAt = time.Now()
			t.resizeW, t.resizeH = cols, lines
		}
		if t.resizeW > 0 && !t.resizeAt.IsZero() &&
			time.Since(t.resizeAt) > 400*time.Millisecond {
			t.cols, t.lines = t.resizeW, t.resizeH
			t.resizeW, t.resizeH = 0, 0
			t.rows = t.lines - 1
			if t.chrome {
				t.rows = t.lines - 2
			}
			if t.rows < 4 {
				t.rows = 4
			}
			if t.cols > 4 {
				t.cap.ResizeTerm(t.cols, t.rows*2)
				t.prevRows = nil
				t.prevTop, t.prevBot = "", ""
				t.renderer.Reset()
			}
		}
		if t.rows == 0 {
			continue
		}
		// frame draw at interval — decode, fit (contain/cover/fill), render
		if img, seq, ok := t.cap.Latest(); ok && seq != frameSeq &&
			time.Since(lastDraw) >= interval {
			frameSeq = seq
			lastDraw = time.Now()
			img2, err := jpeg.Decode(bytes.NewReader(img))
			if err == nil {
				t0 := time.Now()
				rows := t.renderer.Render(img2, t.cols, t.rows)
				ms := float64(time.Since(t0).Microseconds()) / 1000.0

				// fps EMA
				dt := time.Since(t.lastFrameT).Seconds()
				t.lastFrameT = time.Now()
				if dt > 0 {
					inst := 1.0 / dt
					if t.fpsEMA == 0 {
						t.fpsEMA = inst
					} else {
						t.fpsEMA = t.fpsEMA*0.85 + inst*0.15
					}
				}
				_ = ms
				t.draw(rows, ms, t.cap.engine)
				t.frames++
				t.spark = append(t.spark, minF(1.0, ms/50.0))
				if len(t.spark) > 24 {
					t.spark = t.spark[1:]
				}
			}
		}
	}
	t.exit()
}

// exit restores the terminal.
func (t *TUI) exit() {
	restoreTerm(int(os.Stdin.Fd()))
	os.Stdout.WriteString("\x1b[?1049l\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1006l")
	os.Stdout.Sync()
}

// ---------------------------------------------------------------------------
// web app
// ---------------------------------------------------------------------------

type WebApp struct {
	cap      *Capture
	inp      Input
	gpad     *gamepadFeed
	fpsCap   int
	quality  int
	scale    int
	fpsCapMu sync.Mutex
}

func NewWebApp(c *Capture, inp Input) *WebApp {
	w := &WebApp{cap: c, inp: inp, fpsCap: 30, quality: 4, scale: 100}
	if c.useEngine {
		w.gpad = &gamepadFeed{cap: c}
	}
	return w
}

func (w *WebApp) writeJSON(res http.ResponseWriter, v interface{}) {
	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(v)
}

func (w *WebApp) readJSON(req *http.Request, v interface{}) error {
	defer req.Body.Close()
	return json.NewDecoder(req.Body).Decode(v)
}

func (w *WebApp) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", w.page)
	mux.HandleFunc("/stream.fmp4", w.streamFmp4)
	mux.HandleFunc("/stream.mjpg", w.streamMjpg)
	mux.HandleFunc("/audio.ogg", w.streamOgg)
	mux.HandleFunc("/api/status", w.apiStatus)
	mux.HandleFunc("/api/devices", w.apiDevices)
	mux.HandleFunc("/input/tap", w.inputTap)
	mux.HandleFunc("/input/swipe", w.inputSwipe)
	mux.HandleFunc("/input/touch", w.inputTouch)
	mux.HandleFunc("/input/key", w.inputKey)
	mux.HandleFunc("/input/text", w.inputText)
	mux.HandleFunc("/input/audio", w.inputAudio)
	mux.HandleFunc("/input/scroll", w.inputScroll)
	mux.HandleFunc("/input/gamepad", w.inputGamepad)
	mux.HandleFunc("/settings/fps", w.setFPS)
	mux.HandleFunc("/settings/quality", w.setQuality)
	mux.HandleFunc("/settings/scale", w.setScale)
	mux.HandleFunc("/settings/fit", w.setFit)
	mux.HandleFunc("/settings/rotate", w.setRotate)
	mux.HandleFunc("/settings/wake", w.setWake)
	return mux
}

func (w *WebApp) page(res http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" {
		http.NotFound(res, req)
		return
	}
	res.Header().Set("Content-Type", "text/html; charset=utf-8")
	res.Write([]byte(pageHTML))
}

func (w *WebApp) apiStatus(res http.ResponseWriter, req *http.Request) {
	fps, model, bat := w.cap.Stats()
	dw, dh := deviceSize(w.cap.serial)
	w.writeJSON(res, map[string]interface{}{
		"version": VERSION, "serial": w.cap.serial,
		"model": model, "size": []int{dw, dh},
		"battery": bat, "source": w.cap.engine,
		"codec": w.cap.codec, "fps": int(fps + 0.5),
		"tui_fps": float64(len(w.cap.tuiWin)) / 2.0,
		"web_fps": w.fpsCap, "fit": w.cap.fitMode,
	})
}

func (w *WebApp) apiDevices(res http.ResponseWriter, req *http.Request) {
	w.writeJSON(res, map[string]interface{}{"devices": listDevices()})
}

type norm struct{ X, Y float64 }

func pathScale(req *http.Request) (float64, float64) {
	w, h := deviceSize("")
	_ = w
	_ = h
	return 0, 0
}

func normalize(c *Capture, x, y float64) (int, int) {
	dw, dh := deviceSize(c.serial)
	dx := int(x * float64(dw))
	dy := int(y * float64(dh))
	return clamp(dx, 0, dw-1), clamp(dy, 0, dh-1)
}

func (w *WebApp) inputTap(res http.ResponseWriter, req *http.Request) {
	var j struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := w.readJSON(req, &j); err == nil {
		x, y := normalize(w.cap, j.X, j.Y)
		w.inp.Tap(x, y)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputSwipe(res http.ResponseWriter, req *http.Request) {
	var j struct {
		X1, Y1, X2, Y2 float64
	}
	if err := w.readJSON(req, &j); err == nil {
		x1, y1 := normalize(w.cap, j.X1, j.Y1)
		x2, y2 := normalize(w.cap, j.X2, j.Y2)
		w.inp.Swipe(x1, y1, x2, y2, 120)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputTouch(res http.ResponseWriter, req *http.Request) {
	var j struct {
		Act string  `json:"act"`
		X   float64 `json:"x"`
		Y   float64 `json:"y"`
	}
	if err := w.readJSON(req, &j); err == nil {
		x, y := normalize(w.cap, j.X, j.Y)
		switch j.Act {
		case "down":
			w.inp.TouchDown(x, y)
		case "move":
			w.inp.TouchMove(x, y)
		case "up":
			w.inp.TouchUp(x, y)
		}
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputScroll(res http.ResponseWriter, req *http.Request) {
	var j struct {
		X, Y   float64 `json:"x"`
		DX, DY float64 `json:"dx"`
	}
	if err := w.readJSON(req, &j); err == nil {
		if pi, ok := w.inp.(*ProtoInput); ok {
			x, y := normalize(w.cap, j.X, j.Y)
			v := j.DY
			if v == 0 {
				v = j.DX * 0.5
			}
			pi.Scroll(x, y, 0, v*0.06)
		}
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputGamepad(res http.ResponseWriter, req *http.Request) {
	var st engine.GamepadState
	if err := w.readJSON(req, &st); err == nil {
		if g := w.gpad; g != nil {
			g.feed(st)
		}
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputKey(res http.ResponseWriter, req *http.Request) {
	code, _ := strconv.Atoi(req.URL.Query().Get("code"))
	if code == 0 {
		var j struct {
			Code int `json:"code"`
		}
		if err := w.readJSON(req, &j); err == nil {
			code = j.Code
		}
	}
	if code > 0 {
		w.inp.Key(code)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputText(res http.ResponseWriter, req *http.Request) {
	var j struct {
		Text string `json:"text"`
	}
	if err := w.readJSON(req, &j); err == nil {
		w.inp.Text(j.Text)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) inputAudio(res http.ResponseWriter, req *http.Request) {
	// audio is always captured in the mkv; the toggle only exists to keep
	// the API shape; the ogg endpoint streams it live.
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setFPS(res http.ResponseWriter, req *http.Request) {
	var j struct{ Fps int }
	if err := w.readJSON(req, &j); err == nil && j.Fps >= 5 && j.Fps <= 60 {
		w.fpsCapMu.Lock()
		w.fpsCap = j.Fps
		w.fpsCapMu.Unlock()
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setQuality(res http.ResponseWriter, req *http.Request) {
	var j struct{ Q int }
	if err := w.readJSON(req, &j); err == nil {
		w.quality = clamp(j.Q, 1, 10)
		w.cap.jpegQ = w.quality
		w.cap.RespawnFFMpeg()
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setScale(res http.ResponseWriter, req *http.Request) {
	var j struct{ Scale int }
	if err := w.readJSON(req, &j); err == nil {
		w.scale = clamp(j.Scale, 25, 100)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setFit(res http.ResponseWriter, req *http.Request) {
	var j struct{ Fit string }
	if err := w.readJSON(req, &j); err == nil {
		for _, m := range FIT_MODES {
			if m == j.Fit {
				w.cap.SetFit(m)
				break
			}
		}
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setRotate(res http.ResponseWriter, req *http.Request) {
	var j struct{ Deg int }
	if err := w.readJSON(req, &j); err == nil {
		setRotation(w.cap.serial, j.Deg%360)
	}
	w.writeJSON(res, map[string]bool{"ok": true})
}

func (w *WebApp) setWake(res http.ResponseWriter, req *http.Request) {
	wakeUnlock(w.cap.serial, true)
	w.writeJSON(res, map[string]bool{"ok": true})
}

// streamFmp4 serves the live fragmented MP4 (one consumer; reconnect on EOF).
func (w *WebApp) streamFmp4(res http.ResponseWriter, req *http.Request) {
	ch := w.cap.fmp4.Sub()
	fl := res.(http.Flusher)
	res.Header().Set("Content-Type", "video/mp4")
	res.Header().Set("Cache-Control", "no-cache")
	if init := w.cap.fmp4Init; init != nil {
		res.Write(init)
		fl.Flush()
	}
	for {
		select {
		case chunk := <-ch:
			res.Write(chunk)
			fl.Flush()
		case <-req.Context().Done():
			return
		}
	}
}

// streamMjpg serves multipart MJPEG from the latest-frame cache.
func (w *WebApp) streamMjpg(res http.ResponseWriter, req *http.Request) {
	fl := res.(http.Flusher)
	res.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	res.Header().Set("Cache-Control", "no-cache")
	lastSeq := uint64(0)
	pace := time.Second / time.Duration(w.fpsCap)
	lastSent := time.Now()
	fpsMu := &w.fpsCapMu
	for {
		img, seq, ok := w.cap.WebLatest()
		if !ok {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if seq == lastSeq {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		fpsMu.Lock()
		pace2 := time.Second / time.Duration(w.fpsCap)
		fpsMu.Unlock()
		if time.Since(lastSent) < pace || time.Since(lastSent) < pace2 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		lastSeq = seq
		lastSent = time.Now()
		res.Write([]byte("--frame\r\nContent-Type: image/jpeg\r\n\r\n"))
		res.Write(img)
		res.Write([]byte("\r\n"))
		fl.Flush()
	}
}

// streamOgg serves the live Ogg Opus stream (one consumer).
func (w *WebApp) streamOgg(res http.ResponseWriter, req *http.Request) {
	fl := res.(http.Flusher)
	res.Header().Set("Content-Type", "audio/ogg")
	res.Header().Set("Cache-Control", "no-cache")
	// WAIT for the Ogg init (OpusHead) — if the device is silent when the
	// browser connects, ffmpeg hasn't muxed headers yet; subscribing now
	// would stream headerless pages and the browser plays nothing.
	deadline := time.Now().Add(20 * time.Second)
	for w.cap.oggInit == nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	ch := w.cap.ogg.Sub()
	if init := w.cap.oggInit; init != nil {
		res.Write(init)
		fl.Flush()
	}
	for {
		select {
		case chunk := <-ch:
			res.Write(chunk)
			fl.Flush()
		case <-req.Context().Done():
			return
		}
	}
}

const pageHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>scterm · device stream</title>
<style>
:root{--bg:#07080c;--panel:rgba(255,255,255,.05);--panel2:rgba(255,255,255,.09);
--line:rgba(255,255,255,.09);--dim:#8d93a0;--txt:#e9ebf0;--acc:#00e0b4;--acc2:#5b8cff;
--danger:#ff5d73;--warn:#ffc65c}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{background:var(--bg);color:var(--txt);font:14px/1.4 ui-sans-serif,system-ui,"Segoe UI",Roboto,Inter,Arial,sans-serif;
overflow:hidden;-webkit-font-smoothing:antialiased}
body::before{content:"";position:fixed;inset:0;pointer-events:none;z-index:0;
background:radial-gradient(900px 500px at 15% -10%,rgba(0,224,180,.14),transparent 60%),
radial-gradient(800px 500px at 110% 10%,rgba(91,140,255,.12),transparent 55%)}
#app{position:relative;z-index:1;height:100%;display:flex;flex-direction:column}
/* ---------- top bar ---------- */
#bar{display:flex;align-items:center;gap:8px;padding:10px 14px;flex-wrap:wrap;
background:rgba(14,16,22,.72);backdrop-filter:blur(14px) saturate(1.2);
-webkit-backdrop-filter:blur(14px) saturate(1.2);border-bottom:1px solid var(--line)}
.logo{font-weight:800;letter-spacing:.4px;font-size:15px;
background:linear-gradient(90deg,var(--acc),var(--acc2));-webkit-background-clip:text;background-clip:text;color:transparent}
.chip{display:inline-flex;align-items:center;gap:6px;padding:4px 11px;border-radius:999px;
background:var(--panel);border:1px solid var(--line);font-size:12px;color:var(--txt);white-space:nowrap}
.chip b{font-variant-numeric:tabular-nums;font-weight:650}
.dot{width:7px;height:7px;border-radius:50%;background:#3a414d;flex:none}
.dot.live{background:var(--acc);animation:breath 1.6s ease-in-out infinite}
@keyframes breath{0%,100%{opacity:1;box-shadow:0 0 0 0 rgba(0,224,180,.5)}50%{opacity:.55;box-shadow:0 0 0 4px rgba(0,224,180,0)}}
.btn{display:inline-flex;align-items:center;gap:6px;padding:6px 14px;border-radius:999px;
border:1px solid var(--line);background:var(--panel2);color:var(--txt);cursor:pointer;
font:inherit;font-size:12.5px;transition:all .15s ease;user-select:none}
.btn:hover{background:rgba(255,255,255,.14);transform:translateY(-1px)}
.btn:active{transform:translateY(0)}
.btn.acc{background:linear-gradient(90deg,var(--acc),#00b78f);border:0;color:#031613;font-weight:700}
.btn.acc:hover{filter:brightness(1.12)}
.btn.on{background:linear-gradient(90deg,var(--warn),#e0962f);border:0;color:#221503;font-weight:700}
#spacer{flex:1}
/* sliders */
#settings{display:flex;gap:16px;align-items:center}
.sld{display:flex;flex-direction:column;align-items:center;gap:3px;font-size:11px;color:var(--dim)}
.sld label{display:flex;gap:5px;align-items:center}
.sld .val{color:var(--acc);font-weight:700;font-variant-numeric:tabular-nums}
input[type=range]{-webkit-appearance:none;appearance:none;width:104px;height:4px;border-radius:2px;
background:linear-gradient(90deg,var(--acc),var(--acc2));outline:none;cursor:pointer}
input[type=range]::-webkit-slider-thumb{-webkit-appearance:none;width:14px;height:14px;border-radius:50%;
background:#fff;border:3px solid var(--acc);box-shadow:0 1px 6px rgba(0,0,0,.5)}
input[type=range]::-moz-range-thumb{width:14px;height:14px;border-radius:50%;background:#fff;border:3px solid var(--acc)}
/* text input */
#textin{background:rgba(255,255,255,.07);border:1px solid var(--line);color:var(--txt);
padding:6px 13px;border-radius:999px;width:190px;font:inherit;font-size:12.5px;outline:none;transition:border .15s}
#textin:focus{border-color:var(--acc)}
/* ---------- stage ---------- */
#wrap{flex:1;display:flex;align-items:center;justify-content:center;position:relative;overflow:hidden;
background:radial-gradient(700px 400px at 50% 40%,rgba(255,255,255,.03),transparent 70%)}
#wrap video,#wrap img{position:absolute;width:100%;height:100%;object-fit:contain;
border-radius:10px;box-shadow:0 10px 60px rgba(0,0,0,.55),0 0 0 1px rgba(255,255,255,.06)}
#wrap img{display:block;opacity:1;transition:opacity .3s}
#wrap video{opacity:0;transition:opacity .3s}
#wrap video.on{opacity:1}
#wrap img.off{opacity:0;pointer-events:none}
#wrap.live video.on,#wrap.live img:not(.off){box-shadow:0 10px 60px rgba(0,0,0,.55),0 0 26px rgba(0,224,180,.18),0 0 0 1px rgba(0,224,180,.25)}
/* connecting overlay */
#noconn{position:absolute;inset:0;display:flex;flex-direction:column;gap:14px;align-items:center;justify-content:center;
color:var(--dim);background:rgba(7,8,12,.5);backdrop-filter:blur(4px);transition:opacity .4s}
#noconn.hide{opacity:0;pointer-events:none}
.ring{width:52px;height:52px;border-radius:50%;border:3px solid rgba(255,255,255,.08);border-top-color:var(--acc);
animation:spin 1s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
#noconn h2{font-size:15px;color:var(--txt);font-weight:650}
#noconn p{font-size:12px;max-width:340px;text-align:center}
/* footer */
#foot{padding:6px 14px;font-size:11px;color:#5d636e;text-align:center;border-top:1px solid rgba(255,255,255,.05)}
#foot b{color:var(--dim);font-weight:600}
</style></head><body>
<div id="app">
<div id="bar">
  <span class="logo">scterm</span>
  <span class="chip"><span class="dot" id="liveC"></span> <b id="liveT">connecting</b></span>
  <span class="chip">⚡ <b id="fps">--</b><span style="color:var(--dim)">fps</span></span>
  <span class="chip">📐 <b id="res">--×--</b></span>
  <span class="chip">🔋 <b id="bat">-</b></span>
  <span class="chip">🧩 <b id="cod">-</b></span>
  <div id="settings">
    <div class="sld"><label>FPS <span class="val" id="fv">30</span></label><input id="fr" type="range" min="5" max="60" value="30" step="1"></div>
    <div class="sld"><label>QUALITY <span class="val" id="qv">4</span></label><input id="qr" type="range" min="1" max="10" value="4" step="1"></div>
  </div>
  <input type="text" id="textin" placeholder="type text · Enter sends" autocomplete="off" spellcheck="false">
  <button class="btn" id="gbtn">⌨ Keyboard</button>
  <button class="btn" id="abtn" style="display:none">🔈 Audio</button>
  <button class="btn" id="fbtn">⛶ Fullscreen</button>
</div>
<div id="wrap">
  <video id="v" playsinline></video>
  <img id="vimg" draggable="false" alt="">
  <div id="noconn">
    <div class="ring"></div>
    <h2 id="nct">connecting to device…</h2>
    <p id="ncp">waiting for the stream — make sure scterm is running with <b style="color:var(--acc)">--web</b> and the device is attached.</p>
  </div>
</div>
<div id="foot">scterm <b id="ver">3.0</b> · serial <b id="ser">-</b> · stream <b id="src">-</b> · glass-to-glass via scrcpy engine</div>
</div>
<script>
const $=id=>document.getElementById(id);
let st=null,grab=false,audioOn=false,usingMSE=false;
async function api(p,body){try{await fetch(p,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})});}catch(e){}}
/* ---- status ---- */
async function pollStatus(){
  try{
    const j=await(await fetch('/api/status')).json();st=j;
    $('res').textContent=j.size?j.size[0]+'×'+j.size[1]:'--×--';
    $('bat').textContent=j.battery>=0?j.battery+'%':'-';
    $('cod').textContent=j.codec||'mjpg';
    $('fps').textContent=j.fps;
    $('ver').textContent=j.version||'3.0';
    $('ser').textContent=j.serial||'-';
    $('src').textContent=j.source||'-';
    if(j.serial){$('noconn').classList.add('hide');$('liveC').classList.add('live');$('liveT').textContent='LIVE';}
    else{$('noconn').classList.remove('hide');$('liveC').classList.remove('live');$('liveT').textContent='connecting';}
  }catch(e){}
}
setInterval(pollStatus,2500);pollStatus();
/* ---- transport: MSE h264 when possible, else MJPEG ---- */
const mseOK=window.MediaSource&&MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"');
function useMSE(){
  usingMSE=true;
  // live mjpg stays as a backdrop (static screens keep updating via the
  // filler); the video fades in over it once real frames are playing
  $('vimg').src='/stream.mjpg';
  $('v').addEventListener('playing',()=>{$('v').classList.add('on');$('vimg').classList.add('off');});
  const ms=new MediaSource();
  $('v').src=URL.createObjectURL(ms);
  ms.addEventListener('sourceopen',async()=>{
    const codec='video/mp4; codecs="'+((st&&st.codec)||'avc1.42E01E')+'"';
    let sb;
    try{sb=ms.addSourceBuffer(codec);}catch(e){fallbackMJPEG();return;}
    try{
      const resv=await fetch('/stream.fmp4');
      const reader=resv.body.getReader();
      while(true){
        const {value,done}=await reader.read();
        if(done)break;
        if(sb.updating)await new Promise(r=>setTimeout(r,60));
        try{sb.appendBuffer(value);}catch(e){}
      }
    }catch(e){}
    try{$('v').play().catch(()=>{});}catch(e){}
  });
}
function useMJPEG(){usingMSE=false;$('v').classList.remove('on');$('vimg').classList.remove('off');$('vimg').src='/stream.mjpg';}
function fallbackMJPEG(){try{$('v').src='';}catch(e){}useMJPEG();}
// DEFAULT TRANSPORT: MJPEG. The MSE fMP4 path only delivers fragments
// at keyframes (2s, and some ROMs ignore the option entirely), so during
// a drag the <video> froze while the live mjpg backdrop ran at 28fps —
// that was the 2-3s feedback lag. MJPEG updates every frame (~35ms).
// (MSE remains available for a future smooth-playback toggle.)
useMJPEG();
/* ---- pointer: live touch ---- */
function pos(e){
  const el=usingMSE?$('v'):$('vimg');
  const r=el.getBoundingClientRect();
  const t=e.touches?e.touches[0]:e;
  const iw=el.naturalWidth||((st&&st.size&&st.size[0])||1920);
  const ih=el.naturalHeight||((st&&st.size&&st.size[1])||1080);
  const sc=Math.min(r.width/iw,r.height/ih);
  const cw=iw*sc,ch=ih*sc;
  const x=(t.clientX-r.left-(r.width-cw)/2)/cw;
  const y=(t.clientY-r.top-(r.height-ch)/2)/ch;
  return {x:Math.max(0,Math.min(1,x)),y:Math.max(0,Math.min(1,y))};
}
const wrap=$('wrap');
wrap.addEventListener('pointerdown',e=>{
  if(grab){$('v').requestPointerLock&&$('v').requestPointerLock();}
  api('/input/touch',{act:'down',...pos(e)});
  $('wrap').classList.add('live');
  e.preventDefault();
});
// throttle moves to ~60ms: the device-side 'input motionevent' takes
// 100-300ms each; flooding it queues seconds of backlog (the 2-3s drag lag)
let lastMoveT=0,lastP=null;
wrap.addEventListener('pointermove',e=>{
  if(e.buttons!==1)return;
  lastP=pos(e);
  const now=performance.now();
  if(now-lastMoveT>60){api('/input/touch',{act:'move',...lastP});lastMoveT=now;}
});
wrap.addEventListener('pointerup',e=>{
  // flush the final position so the device ends the gesture where we are
  if(lastP){let p=pos(e);api('/input/touch',{act:'move',...p});}
  api('/input/touch',{act:'up',...pos(e)});lastP=null;
});
wrap.addEventListener('wheel',e=>{
  const p=pos(e);
  api('/input/scroll',{x:p.x,y:p.y,dx:e.deltaX,dy:e.deltaY});
  e.preventDefault();
},{passive:false});
/* ---- gamepad (viewer controller -> device) ---- */
(function(){
  let last=null;
  setInterval(()=>{
    const gp=(navigator.getGamepads&&navigator.getGamepads())||[];
    const g=gp[0]; if(!g)return;
    const b=g.buttons,ax=g.axes;
    const btn=i=>b[i]&&b[i].pressed;
    const st={
      lx:ax[0]||0,ly:ax[1]||0,rx:ax[2]||0,ry:ax[3]||0,
      l2:ax[4]||0,r2:ax[5]||0,
      a:btn(0),b:btn(1),x:btn(2),y:btn(3),
      lb:btn(4),rb:btn(5),back:btn(8),start:btn(9),guide:btn(10),
      ls:btn(11),rs:btn(12),
      hat:btn(12)?0:(btn(13)?8:0)
    };
    const d=(btn(14))||(btn(15))||(btn(16))||(btn(17));
    if(d){st.hat=btn(14)?8:btn(15)?5:btn(13)?1:btn(17)?2:btn(16)?7:0;}
    const k=JSON.stringify(st);
    if(k!==last){last=k;api('/input/gamepad',JSON.parse(k));}
  },50);
})();
/* ---- keyboard ---- */
function fmap(k){return {F1:82,F2:3,F3:4,F4:187,F5:26,F6:24,F7:25,F8:23,F9:19,F10:20,F11:21}[k]||0;}
document.addEventListener('keydown',e=>{
  if(e.key.startsWith('F')||e.ctrlKey){
    e.preventDefault();
    const c=fmap(e.key);
    if(c)api('/input/key',{code:c});
  }
  if(e.key==='Escape'&&grab)toggleGrab();
});
document.addEventListener('pointerlockchange',()=>{if(!document.pointerLockElement)grab=false;});
$('textin').addEventListener('keydown',e=>{
  if(e.key==='Enter'&&e.target.value){api('/input/text',{text:e.target.value});e.target.value='';}
});
/* ---- controls ---- */
async function toggleGrab(){grab=!grab;$('gbtn').classList.toggle('on',grab);$('gbtn').textContent=grab?'⌨ Keys live':'⌨ Keyboard';}
$('gbtn').addEventListener('click',toggleGrab);
$('fbtn').addEventListener('click',()=>{
  if(document.fullscreenElement)document.exitFullscreen();
  else document.documentElement.requestFullscreen();
});
async function toggleAudio(){
  audioOn=!audioOn;
  $('abtn').classList.toggle('on',audioOn);
  $('abtn').textContent=audioOn?'🔊 Audio on':'🔈 Audio';
  if(audioOn){
    const a=document.createElement('audio');a.autoplay=true;a.src='/audio.ogg';a.id='aud';a.style.display='none';document.body.appendChild(a);
    a.play().catch(()=>{});
  }else{
    const a=document.getElementById('aud');if(a){a.pause();a.remove();}
  }
}
$('abtn').addEventListener('click',toggleAudio);
$('abtn').style.display='';
/* sliders */
$('fr').addEventListener('change',e=>{$('fv').textContent=e.target.value;api('/settings/fps',{fps:+e.target.value});});
$('qr').addEventListener('change',e=>{$('qv').textContent=e.target.value;api('/settings/quality',{q:+e.target.value});});
</script></body></html>
`

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func listDevices() []string {
	devs := []string{}
	out := hostOut("devices")
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 {
			state := f[1]
			if state == "device" || state == "unauthorized" {
				devs = append(devs, f[0])
			}
		}
	}
	return devs
}

func main() {
	var (
		tui     bool
		web     bool
		webPort = 8000
		window  bool
		serial  string
		fps     int
		fit     string
		maxSize int
		bitrate int
		jpegQ   int
		wake    bool
		stay    bool
		useEngine bool
	)
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--engine":
			useEngine = true
		case a == "--tui":
			tui = true
		case a == "--web":
			web = true
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					webPort = n
					i++
				}
			}
		case strings.HasPrefix(a, "--web="):
			web = true
			webPort, _ = strconv.Atoi(strings.TrimPrefix(a, "--web="))
		case a == "--both":
			tui, web = true, true
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					webPort = n
					i++
				}
			}
		case strings.HasPrefix(a, "--both="):
			tui, web = true, true
			webPort, _ = strconv.Atoi(strings.TrimPrefix(a, "--both="))
		case a == "--window":
			window = true
		case a == "-s" || a == "--serial":
			serial = next()
		case strings.HasPrefix(a, "--serial="):
			serial = strings.TrimPrefix(a, "--serial=")
		case a == "--fps":
			fps, _ = strconv.Atoi(next())
		case a == "--fit":
			fit = next()
		case a == "--max-size":
			maxSize, _ = strconv.Atoi(next())
		case a == "--bit-rate":
			bitrate, _ = strconv.Atoi(next())
		case a == "-q" || a == "--jpeg-quality":
			jpegQ, _ = strconv.Atoi(next())
		case a == "--no-wake":
			wake = false
		case a == "--no-stay-awake":
			stay = false
		case a == "-h" || a == "--help":
			usage()
			return
		case a == "--version":
			fmt.Println("scterm", VERSION)
			return
		}
	}
	if fps == 0 {
		fps = 18
	}
	if maxSize == 0 {
		maxSize = 1280
	}
	if bitrate == 0 {
		bitrate = 8000000
	}
	if jpegQ == 0 {
		jpegQ = 4
	}
	if fit == "" {
		fit = "contain"
	}

	// serial resolution
	if serial == "" {
		devs := listDevices()
		if len(devs) > 0 {
			serial = devs[0]
		}
	}

	// --window: pure scrcpy passthrough (its own window + control)
	if window {
		cmdArgs := []string{"-s", serial}
		override := os.Getenv("SCRCPY_ARGS")
		if override != "" {
			cmdArgs = strings.Fields(override)
		}
		cmd := exec.Command("scrcpy", cmdArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Run()
		return
	}

	if serial == "" {
		logf("no adb device found")
		os.Exit(1)
	}

	cap := NewCapture(serial, maxSize, bitrate, jpegQ)
	cap.fpsCap = fps
	cap.fitMode = fit
	cap.useEngine = useEngine
	var inp Input
	if useEngine {
		pi := &ProtoInput{serial: serial, ctrl: nil}
		cap.protoIn = pi
		inp = pi
	} else {
		inp = NewAdbInput(serial)
	}

	if wake {
		wakeUnlock(serial, stay)
	}
	setRotation(serial, 0)
	cap.Start()

	if useEngine {
		// TUI-side gamepad: host evdev joystick -> UHID gamepad on the
		// device (silently no-op if no gamepad is attached to this PC).
		go func() {
			for {
				gr, err := engine.OpenGamepadReader()
				if err != nil {
					return
				}
				feed := &gamepadFeed{cap: cap}
				gr.ReadLoop(func(st engine.GamepadState) { feed.feed(st) })
				time.Sleep(2 * time.Second)
			}
		}()
	}

	if web {
		app := NewWebApp(cap, inp)
		app.fpsCap = fps
		go func() {
			addr := fmt.Sprintf("0.0.0.0:%d", webPort)
			logf("web viewer on http://%s (or http://127.0.0.1:%d)", addr, webPort)
			if err := http.ListenAndServe(addr, app.Handler()); err != nil {
				logf("web: %v", err)
			}
		}()
	}

	if tui || !web {
		t := NewTUI(cap, inp, fit, true)
		// capture must know the terminal size for the mjpeg vf
		cols, lines := getTermSize()
		t.cols, t.lines = cols, lines
		t.rows = lines - 2
		if t.rows < 4 {
			t.rows = 4
		}
		cap.ResizeTerm(cols, t.rows*2)
		defer t.exit()
		t.Run()
	} else {
		// web only: keep alive until signal
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		<-ch
	}
	cap.Close()
	inp.Close()
}

func usage() {
	fmt.Print(`scterm ` + VERSION + ` — Android remote control (Go)

Usage: scterm [flags]

  --tui            terminal UI (default if stdin is a tty)
  --web[=PORT]     web viewer only (default port 8000)
  --both[=PORT]    TUI + web
  --window         standalone scrcpy-style window (passthrough to scrcpy)
  -s SERIAL        device serial (default: first adb device)
  --fps N          TUI refresh cap (default 30)
  --fit MODE       contain | cover | fill (default contain)
  --max-size N     stream max dimension (default 1280)
  --bit-rate N     video bitrate in bps (default 8000000)
  -q N             MJPEG quality 1-10 (default 4)
  --no-wake        don't wake the device on start
  --version        print version

Env: SCRCPY_ARGS — extra args passed to the scrcpy engine.
`)
}
