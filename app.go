package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type app struct {
	sess     *session
	cfg      config
	tui      *tui
	ctrl     *controller
	audio    *audioSink
	stream   *streamState
	web      *webServer
	grabbed  bool
	inZellij bool

	// input events channel
	events chan inputEvent

	// frames is the display mailbox between the video demux goroutine and the
	// run loop: one slot, drop-old. The demux goroutine hands frames over here
	// and never waits, so a slow terminal (or a stalled reader behind it) drops
	// frames instead of stalling the h264 decoder and backing the device stream
	// up. Drawing must therefore happen in the run loop, never in the sink.
	frames chan *videoFrame

	// software keyboard
	kb *keyboard

	// action menu: the terminal's discoverable control list (menu.go). Open
	// state and the painted row layout, so a click can be mapped back to the
	// row it landed on without recomputing the overlay.
	menuOpen bool
	menuHits []menuHit

	// action bar: the mouse-driven button strip (actionbar.go). Show state is
	// driven by recent mouse activity and a hide timer checked on the tick;
	// barHits is the painted layout clicks are matched against,
	// barTopRow/barBotRow the screen rows it actually painted on (0 = not
	// drawn), barPointerY the last motion row (so a pointer resting on the
	// strip keeps it up), and barFlashIdx/At give a clicked button a brief
	// green flash.
	barShow         bool
	barLastActivity int64
	barOffset       int
	barHits         []barHit
	barTopRow       int
	barBotRow       int
	barPointerY     int
	barFlashIdx     int
	barFlashAt      int64

	// pointer-drag state: button, coalescer clock and the pending move.
	// Owned by the run loop in the terminal, but written by the control
	// dispatcher goroutine in --web/--window, so it is self-locking.
	drag dragState

	// frame geometry for pointer mapping
	frameW, frameH int

	// frametime history for the status-bar sparkline (latest last)
	ftHist [96]float64 // ms per frame
	ftIdx  int
	ftCnt  int

	lastFrameNano int64

	// keyboard auto-grabbed the mouse on open (Zellij); restore on close
	kbAutoGrabbed bool

	// quitting is set once shutdown has begun, so teardown-time socket errors
	// ("stream ended: use of closed network connection") are not reported as
	// failures on the way out.
	quitting atomic.Bool

	// audioOff is set once the device has turned the audio stream off (see
	// playAudio): the status row then shows no volume.
	audioOff atomic.Bool

	// restoreTerm gives the terminal back (normal screen, no mouse reporting,
	// cooked mode) once, whichever of the run loop's exit and a panic on
	// another goroutine gets there first. Nil outside the terminal UI.
	restoreTerm func()
}

type inputEvent struct {
	kind int // evBytes, evResize, evQuit, evTick, evTickFast, evZellijRegrab
	buf  []byte
}

const (
	evBytes        = 0
	evResize       = 1
	evQuit         = 2
	evTick         = 3
	evTickFast     = 4 // ~16ms: flushes coalesced drag moves without waiting for evTick
	evZellijRegrab = 5 // openKeyboard's delayed mouse re-grab, done on the run loop
)

func newApp(sess *session, cfg config) *app {
	a := &app{
		sess:        sess,
		cfg:         cfg,
		audio:       newAudioSink(),
		events:      make(chan inputEvent, 256),
		frames:      make(chan *videoFrame, 1), // drop-old display mailbox
		grabbed:     true,
		barFlashIdx: -1, // no button flashed until one is clicked
	}
	_, a.inZellij = os.LookupEnv("ZELLIJ")
	if a.inZellij {
		a.grabbed = false
	}
	if cfg.control && sess.control != nil {
		a.ctrl = newController(sess.control)
		go deviceMsgReader(sess.control)
	}
	// The demux loops live for as long as the app; they must exist before the
	// web server (which sinks decoded frames) is built.
	a.stream = newStreamState(sess, cfg, a.ctrl)
	if !cfg.noTUI {
		a.tui = newTUI()
		a.tui.repaintInterval = cfg.repaintInterval
		a.kb = newKeyboard()
	}
	if cfg.web {
		// The browser is the display: no TUI, no terminal mouse grab, no
		// alternate screen. The terminal stays a normal terminal and only
		// shows log lines.
		a.tui = nil
		a.kb = nil
		a.web = newWebServer(cfg, sess, a.stream, a.audio, a.ctrl, a)
		a.stream.setSink(a.web)
	} else {
		// TUI (and headless) runs consume frames in their own run loop, so the
		// app is the sink. Without this the stream has nowhere to deliver to
		// and the display stays black -- which is exactly what happened when
		// the sink refactor left this unwired.
		a.stream.setSink(a)
	}
	return a
}

// frame is the frameSink implementation for the terminal UI and for headless
// runs. It runs ON the video demux goroutine, so it must never block: the frame
// goes into a one-slot mailbox with drop-old semantics and the run loop draws
// it. Drawing here instead would make a slow terminal stall the h264 decoder
// (and with it the device-side stream), which is the opposite of what the
// display path is for.
func (a *app) frame(f *videoFrame) {
	if f == nil {
		return
	}
	select {
	case a.frames <- f:
	default:
		// drop-old: replace a queued frame with the freshest one, recycling
		// the buffer we just evicted.
		select {
		case old := <-a.frames:
			a.stream.returnPooled(old.rgb)
		default:
		}
		a.frames <- f
	}
}

func (a *app) run() error {
	if a.web != nil {
		return a.runWeb()
	}
	if a.tui == nil {
		return a.runHeadless()
	}

	oldMode, err := setRawMode(ttyReadFD())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	a.restoreTerm = a.terminalRestorer(oldMode)
	defer a.restoreTerm()

	// Print warnings before entering the alternate screen so they don't
	// corrupt the TUI.
	if a.cfg.audio && a.audio.err != nil {
		fmt.Fprintf(stderrWriter(), "scterm: audio disabled: %v\n", a.audio.errString())
	}

	// From here until the terminal is restored, messages are notices.
	wake := notices.start()
	a.tui.shellInit()

	a.setMouse(a.grabbed)
	a.tui.setStatus(a.statusLine())
	a.paintPlaceholder()

	a.goTUI(a.inputLoop)
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	a.goTUI(func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				a.events <- inputEvent{kind: evResize}
			default:
				a.events <- inputEvent{kind: evQuit}
			}
		}
	})

	lost := a.watchStreams()

	a.goTUI(func() {
		for {
			time.Sleep(500 * time.Millisecond)
			a.events <- inputEvent{kind: evTick}
		}
	})
	a.goTUI(func() {
		fast := time.NewTicker(16 * time.Millisecond)
		defer fast.Stop()
		for range fast.C {
			a.events <- inputEvent{kind: evTickFast}
		}
	})
	a.goTUI(a.followAudioSink)

	return a.loop(wake, lost)
}

// loop is the terminal UI's run loop. It ends with nil on a quit, or with the
// error of the stream the terminal UI shows when that stream ends.
func (a *app) loop(wake <-chan struct{}, lost <-chan error) error {
	for {
		select {
		case ev := <-a.events:
			switch ev.kind {
			case evQuit:
				return nil
			case evResize:
				a.onResize()
			case evTickFast:
				a.flushPendingMove()
			case evTick:
				a.flushPendingMove()
				a.barTick()
				a.kbTick(timeNowUnixNano())
				a.tui.setStatus(a.statusLine())
			case evBytes:
				a.handleInput(ev.buf)
			case evZellijRegrab:
				a.zellijRegrab()
			}
		case f := <-a.frames:
			// Draw here, in the run loop, not in the sink: see app.frame. This
			// is the only place a frame is rendered, and it must be identical
			// in structure to what the TUI has always done.
			now := timeNowUnixNano()
			if a.lastFrameNano != 0 {
				a.recordFrameTime(float64(now-a.lastFrameNano) / 1e6)
			}
			a.lastFrameNano = now
			a.frameW, a.frameH = f.w, f.h
			a.tui.draw(f.rgb)
			a.stream.returnPooled(f.rgb)
		case <-wake:
			a.refreshStatus()
		case err := <-lost:
			return err
		}
	}
}

// terminalRestorer gives the terminal back (normal screen, no mouse
// reporting, cooked mode) once, whichever of the run loop's exit and a panic
// on another goroutine gets there first, then prints the notices the terminal
// UI showed. From then on the app is quitting.
func (a *app) terminalRestorer(oldMode *syscall.Termios) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			a.quitting.Store(true)
			a.tui.shellClose()
			restoreMode(ttyReadFD(), oldMode)
			notices.stop(stderrWriter())
		})
	}
}

// watchStreams starts the demuxers. The video stream, or the audio stream when
// there is no video, is what the terminal UI shows: when it ends, the session
// is over, and the error naming the device comes back on the channel for the
// run loop to return. The channel has room for both, so a demuxer never waits
// on a run loop that has already gone.
func (a *app) watchStreams() <-chan error {
	lost := make(chan error, 2)
	a.goTUI(func() {
		if err := a.stream.runVideo(); err != nil {
			lost <- a.lostStream("video", err)
		}
	})
	a.goTUI(func() {
		err := a.playAudio()
		switch {
		case err == nil:
		case a.sess.video == nil:
			lost <- a.lostStream("audio", err)
		case a.quitting.Load() && isClosedConnErr(err):
			// sess.stop closed the socket on the way out. (Not before: the
			// device ending the audio stream, EOF, is worth a notice.)
		default:
			noticef("scterm: audio: %v\n", err)
		}
	})
	return lost
}

// playAudio runs the audio demuxer and returns how the audio stream ended.
// When the device turns the stream off, the session goes on without sound, as
// in scrcpy: playAudio says so, with the server's reason, and returns nil.
func (a *app) playAudio() error {
	err := a.stream.runAudio(a.audio)
	if !errors.Is(err, errAudioOff) {
		return err
	}
	a.audioOff.Store(true)
	line := "scterm: no sound from " + a.deviceName()
	if reason := a.sess.audioOffReason(audioOffWait); reason != "" {
		line += ": " + reason
	}
	if a.cfg.audioDup && a.cfg.audioSource == "" && !a.cfg.usePeer {
		// serverParams asked for the playback source.
		line += " (--audio-dup implies --audio-source playback, which needs Android 13 or later)"
	}
	notice(line + "\n")
	return nil
}

// lostStream is the error the terminal UI ends with when the stream it shows
// ends without a quit.
func (a *app) lostStream(stream string, err error) error {
	return fmt.Errorf("lost the %s stream from %s: %w", stream, a.deviceName(), err)
}

// deviceName is the device's name for a sentence: "the device" when its name
// is not known.
func (a *app) deviceName() string {
	if name := a.knownDeviceName(); name != "" {
		return name
	}
	return "the device"
}

// knownDeviceName is the device's name fit for the screen, or "" when it is
// not known. The device or the pairing sets it, so control characters are
// taken out.
func (a *app) knownDeviceName() string {
	if a.sess == nil {
		return ""
	}
	return cleanText(a.sess.deviceName)
}

// paintPlaceholder says what the empty picture is waiting for. The first frame
// paints over it.
func (a *app) paintPlaceholder() {
	a.tui.setPlaceholder(a.placeholderText())
	a.tui.repaint()
}

// placeholderText is the line shown where the picture will be. The terminal UI
// starts once the session is connected, so before the first frame it is
// waiting for video, not for the connection. A peer session has no video
// socket unless the pairing grants view and the device offers video.
func (a *app) placeholderText() string {
	switch {
	case !a.cfg.video:
		return "Video is off (--video=false)"
	case a.sess != nil && a.sess.video == nil:
		return "No video from " + a.deviceName()
	}
	return "Waiting for video from " + a.deviceName() + "…"
}

// goTUI runs fn on its own goroutine while the terminal UI owns the screen. A
// panic there would end the process without run's deferred restore, leaving
// the shell in raw mode on the alternate screen with mouse reporting on.
func (a *app) goTUI(fn func()) {
	go func() {
		defer a.restoreTerminalOnPanic()
		fn()
	}()
}

// restoreTerminalOnPanic gives the terminal back, then lets the panic go on
// to print its stack trace and end the process.
func (a *app) restoreTerminalOnPanic() {
	if r := recover(); r != nil {
		if a.restoreTerm != nil {
			a.restoreTerm()
		}
		panic(r)
	}
}

// hostAudioSilent reports whether the host audio sink must stay silent.
//
// In web/window mode the browser plays the device audio (fed by the PCM tap),
// so the stream is the sole source of sound and the host sink is muted.
// --audio-dup asks for the sound in both places: the host device and the
// window each play it, which is a deliberate duplicate rather than the
// accidental doubling that used to happen unconditionally.
//
// The TUI is unaffected: there the host sink IS the only output, so it always
// plays (and --audio-dup keeps its other meaning, letting the device keep
// playing its own audio while capture runs).
func hostAudioSilent(cfg config) bool {
	return cfg.web && !cfg.audioDup
}

// runWeb is the --web / --window main loop. There is no terminal UI: the
// browser is the display, so this loop only owns the stream goroutines, the
// shutdown signal path and the shutdown sequence.
func (a *app) runWeb() error {
	if a.web == nil {
		return fmt.Errorf("web display not configured")
	}

	// The frame sink (a.web) is wired in newApp, for every mode, so a run can
	// never start with no sink and silently draw nothing.
	//
	// The browser is the display, so the frame canvas is the video's own size
	// rather than the terminal cell grid: every client shares one geometry and
	// scales it with CSS. Set before runVideo starts (the goroutine below), so
	// the demux goroutine always sees it.
	a.stream.webMode = true
	// Audio routing: in web/window mode the browser plays the device audio, so
	// the stream is the sole source of sound and the host sink stays silent.
	// --audio-dup asks for the sound in both places (host device and window),
	// which is the only way to get it played twice.
	if a.audio != nil {
		a.audio.silent.Store(hostAudioSilent(a.cfg))
		switch {
		case !a.cfg.audio:
			// No sound, so nowhere for it to play.
		case a.audio.err != nil:
			// The PCM tap does not go through the sink: the browser still plays.
			fmt.Fprintf(stderrWriter(),
				"scterm: audio: browser only; no sound on this computer: %v\n", a.audio.errString())
		case a.audio.silent.Load():
			// "muted" here is the POLICY, not a reading of the host mixer: the
			// browser is the audio output, so the host sink is deliberately
			// left silent to avoid playing every sound twice, slightly offset.
			fmt.Fprintf(stderrWriter(),
				"scterm: audio: browser only (the host sink stays silent; pass --audio-dup to play on both)\n")
		case a.cfg.audioDup:
			fmt.Fprintf(stderrWriter(),
				"scterm: audio: duplicated (host sink and browser both play)\n")
		}
	}

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for range sigCh {
			select {
			case a.events <- inputEvent{kind: evQuit}:
			default:
			}
		}
	}()

	videoErr := make(chan error, 1)
	go func() {
		if err := a.stream.runVideo(); err != nil {
			if !a.quitting.Load() && !isClosedConnErr(err) {
				fmt.Fprintf(stderrWriter(), "scterm: video: %v\n", err)
			}
			videoErr <- err
		}
	}()
	go func() {
		if err := a.playAudio(); err != nil && !a.quitting.Load() && !isClosedConnErr(err) {
			fmt.Fprintf(stderrWriter(), "scterm: audio: %v\n", err)
		}
	}()
	go a.followAudioSink()

	// A 500ms tick keeps the mouse-drag coalescer fed even when no browser is
	// connected, and lets the status line (TUI-attached runs) stay live.
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			select {
			case a.events <- inputEvent{kind: evTick}:
			case <-a.web.done:
				return
			}
		}
	}()
	go func() {
		fast := time.NewTicker(16 * time.Millisecond)
		defer fast.Stop()
		for {
			select {
			case <-fast.C:
				select {
				case a.events <- inputEvent{kind: evTickFast}:
				default:
				}
			case <-a.web.done:
				return
			}
		}
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- a.web.run(a.cfg.window) }()

	for {
		select {
		case ev := <-a.events:
			switch ev.kind {
			case evQuit:
				a.quitting.Store(true)
				a.web.stop()
				return nil
			case evTickFast:
				a.flushPendingMove()
			case evTick:
				a.flushPendingMove()
				a.refreshStatus()
			case evBytes:
				a.handleInput(ev.buf)
			}
		case err := <-serveErr:
			a.quitting.Store(true)
			a.web.stop()
			return err
		case err := <-videoErr:
			a.quitting.Store(true)
			a.web.stopWith(webByeStreamEnded)
			return err
		}
	}
}

func (a *app) runHeadless() error {
	if a.cfg.audio && a.audio.err != nil {
		fmt.Fprintf(stderrWriter(), "scterm: audio disabled: %v\n", a.audio.errString())
	}
	// Same signal handling as the TUI path: SIGTERM/SIGINT must exit and
	// run shutdown so the PulseAudio stream is closed.
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				// no TUI, ignore
			default:
				a.events <- inputEvent{kind: evQuit}
			}
		}
	}()
	// The end of the video stream ends the run with its error, which main
	// prints ("scterm: video: ...") before it exits.
	videoErr := make(chan error, 1)
	go func() {
		if err := a.stream.runVideo(); err != nil {
			videoErr <- err
		}
	}()
	go func() {
		if err := a.playAudio(); err != nil {
			fmt.Fprintf(stderrWriter(), "scterm: audio: %v\n", err)
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var dumped int

	for {
		select {
		case <-a.events:
			return nil
		case err := <-videoErr:
			return fmt.Errorf("video: %w", err)
		case f := <-a.frames:
			// Same mailbox as the TUI: the run loop owns the frame and the
			// demux goroutine never waits on disk I/O either. At most three
			// frames are written, as before.
			a.frameW, a.frameH = f.w, f.h
			if a.cfg.dumpFrames != "" && dumped < 3 {
				if err := dumpPPM(a.cfg.dumpFrames, dumped, f.rgb, f.cw, f.ch); err != nil {
					fmt.Fprintf(stderrWriter(), "scterm: dump frame: %v\n", err)
				} else {
					dumped++
					fmt.Fprintf(os.Stdout, "dumped frame %d: %dx%d (canvas %dx%d)\n",
						dumped, f.w, f.h, f.cw, f.ch)
				}
			}
			a.stream.returnPooled(f.rgb)
		case <-ticker.C:
			lag := (a.stream.lastVideoPts.Load() - a.stream.lastAudioPts.Load()) / 1000
			fmt.Fprintf(os.Stdout, "\r[%dx%d fps=%.0f audio=%s pcm=%dKB peak=%d avlag=%dms]   ",
				a.frameW, a.frameH, a.stream.currentFPS, audioState(a),
				a.stream.audioBytesSeen()/1024, a.stream.peakAudio(), lag)
		}
	}
}

func audioState(a *app) string {
	if a.audio.err != nil {
		return "off"
	}
	if a.audio.gainPercent() == 0 {
		return "muted"
	}
	return fmt.Sprintf("%d%%", a.audio.gainPercent())
}

// Mouse reporting sequences. 1000 = press/release, 1002 = button motion,
// 1003 = any motion (hover, which wakes the action bar), 1006 = SGR encoding.
//
// 1015 (urxvt encoding) is deliberately NOT enabled with the others: on a
// terminal that implements both, the mode set LAST wins, and with 1006 before
// 1015 that is urxvt -- whose events (CSI b;x;yM, no "<") this client does not
// parse at all. That is why the mouse worked through Zellij (which re-encodes
// to SGR) but did nothing in a plain terminal tab. The disable list still
// turns 1015 off, for terminals left in that mode by something else.
const (
	mouseOnSeq  = "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"
	mouseOffSeq = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l"
)

// setMouse enables/disables terminal mouse tracking. 1003 (any-event motion)
// is included so hovering is reported, which is what wakes the action bar:
// with only 1002 the terminal reports motion while a button is down, and the
// bar could never appear until after a click -- too late, because the first
// click taps the device.
func (a *app) setMouse(on bool) {
	if on {
		os.Stdout.WriteString(mouseOnSeq)
	} else {
		os.Stdout.WriteString(mouseOffSeq)
	}
	a.grabbed = on
}

// onResize handles a terminal resize: the cell grid changes, and any overlay
// is size-dependent. The menu in particular must re-record its click rows
// (refreshMenu does that) or a click would be matched against the old
// geometry, up to and including the status row that runs Quit on a small pane.
// The keyboard is laid out from the bottom of the pane, so it moves with it.
func (a *app) onResize() {
	a.tui.resize()
	if a.stream != nil {
		a.stream.markGeometryDirty()
	}
	switch {
	case a.menuOpen:
		a.refreshMenu()
	case a.kb != nil && a.kb.open:
		a.refreshKeyboard()
	case a.barVisible():
		a.refreshOverlays()
	case !a.tui.hasFrame():
		a.tui.repaint() // the placeholder moves to the middle of the new size
	}
	a.tui.setStatus(a.statusLine())
}

// refreshStatus redraws the status line (nil-safe for headless/transient states).
func (a *app) refreshStatus() {
	if a.tui != nil {
		a.tui.setStatus(a.statusLine())
	}
}

// ---------------------------------------------------------------------------
// software keyboard open/close + overlay refresh
// ---------------------------------------------------------------------------

func (a *app) openKeyboard() {
	if a.kb == nil {
		return
	}
	// The menu and the keyboard are both full-width overlays; opening one
	// closes the other so they cannot share the screen.
	if a.menuOpen {
		a.closeMenu()
	}
	a.kb.open = true
	// In Zellij the app starts ungrabbed (mouse reporting OFF), so clicks
	// never reach us and the keyboard would be useless. Grab on open so
	// clicking works immediately; restore the prior state on close.
	if !a.grabbed {
		a.setMouse(true)
		a.kbAutoGrabbed = true
	}
	// Zellij sometimes needs the pane to be focused/clicked before mouse
	// events flow; the first click after opening may be consumed by the
	// frontend. Re-assert grab briefly after 300ms as a handshake nudge. The
	// timer's goroutine only asks the run loop, which owns the app's state and
	// the screen; a full queue drops the nudge.
	if a.inZellij {
		time.AfterFunc(300*time.Millisecond, func() {
			select {
			case a.events <- inputEvent{kind: evZellijRegrab}:
			default:
			}
		})
	}
	a.refreshKeyboard()
}

// zellijRegrab is openKeyboard's delayed re-grab, run by the run loop. Once
// the terminal has been given back it does nothing: mouse reporting turned on
// then would pour escape sequences into the shell.
func (a *app) zellijRegrab() {
	if a.quitting.Load() || a.kb == nil || !a.kb.open || a.grabbed {
		return
	}
	a.setMouse(true)
	a.kbAutoGrabbed = true
	a.refreshKeyboard()
}

func (a *app) closeKeyboard() {
	if a.kb == nil {
		return
	}
	a.kb.open = false
	// Restore the pre-keyboard grab state (Zellij starts ungrabbed).
	if a.kbAutoGrabbed && a.grabbed {
		a.setMouse(false)
	}
	a.kbAutoGrabbed = false
	if a.tui != nil {
		a.refreshOverlays()
	}
}

func (a *app) toggleKeyboard() {
	if a.kb == nil {
		return
	}
	if a.kb.open {
		a.closeKeyboard()
	} else {
		a.openKeyboard()
	}
}

// refreshKeyboard re-renders the keyboard overlay + status.
func (a *app) refreshKeyboard() {
	if a.kb == nil || a.tui == nil {
		return
	}
	a.refreshOverlays()
}

// ---------------------------------------------------------------------------
// frametime history (status-bar sparkline)
// ---------------------------------------------------------------------------

// recordFrameTime feeds one rendered-frame interval in milliseconds.
func (a *app) recordFrameTime(ms float64) {
	if ms < 0 {
		ms = 0
	}
	if ms > 5000 {
		ms = 5000
	}
	a.ftHist[a.ftIdx] = ms
	a.ftIdx = (a.ftIdx + 1) % len(a.ftHist)
	if a.ftCnt < len(a.ftHist) {
		a.ftCnt++
	}
}

// sparkline renders the frametime history as a tiny bar graph. Target is the
// display refresh target (e.g. 16.7ms at 60Hz); taller than ~60ms = spike.
func (a *app) sparkline(width int) string {
	if a.ftCnt == 0 || width <= 0 {
		return ""
	}
	n := a.ftCnt
	if n > width {
		n = width
	}
	// take the latest n samples, oldest first
	start := a.ftIdx - n
	if start < 0 {
		start += len(a.ftHist)
	}
	// max clamp at 100ms so a single giant spike doesn't flatten the graph
	maxMs := 100.0
	// NOTE: blocks is a []rune. Indexing a string by byte would slice INSIDE
	// a 3-byte UTF-8 sequence and emit garbage (the garbled-characters bug).
	blocks := []rune("▁▂▃▄▅▆▇")
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		v := a.ftHist[(start+i)%len(a.ftHist)]
		if v > maxMs {
			v = maxMs
		}
		// 0..100ms -> bars 1..7. The lowest is a visible bar, so a steady
		// rate draws a flat line rather than blank cells.
		h := int(v / maxMs * 7.0)
		if h < 1 {
			h = 1
		}
		if h > 7 {
			h = 7
		}
		out = append(out, blocks[h-1])
	}
	return string(out)
}

// The status row hints come first and stay whole whenever the pane is as wide
// as them: Ctrl+C goes to the device (raw mode), so how to quit must always be
// readable. They are deliberately short: the full control list is one chord
// away in the Controls menu (and in the README), and spelling out every key
// here ate two lines of a phone-sized terminal.
const (
	statusHints    = "Alt+Q quit · Alt+/ controls"
	statusQuitHint = "Alt+Q quit" // all a pane narrower than the hints keeps
)

// statusPart is one piece of the status row after the hints.
type statusPart struct {
	text   string
	join   string // what goes before it, " · " when empty
	shrink bool   // cut short with "…" instead of dropped when it does not fit
}

// statusMinShrink is the fewest cells a part is cut to before it is dropped.
const statusMinShrink = 6

// statusQuitPrompt leads the parts while an overlay's Quit waits for its
// second press. The menu says so in its Quit row too, but on a pane of 18
// rows or fewer that row is cut off.
const statusQuitPrompt = "press Quit again to quit"

func (a *app) statusLine() string {
	if a.tui == nil || a.audio == nil {
		return ""
	}
	now := timeNowUnixNano()
	parts := a.statusParts()
	if n := notices.current(now); n != "" {
		// A notice stands in for the state and the stats for a few seconds.
		parts = []statusPart{{text: n, shrink: true}}
	}
	if a.kb != nil && a.kb.quit.from != 0 && a.kb.quitArmed(a.kb.quit.from, now) {
		parts = append([]statusPart{{text: statusQuitPrompt, shrink: true}}, parts...)
	}
	return fitStatus(a.tui.cols, parts)
}

// statusParts is what follows the hints, most important first: where input
// goes, the software keyboard, the volume and the device's name if it is
// known, then the frame size and rate and their sparkline, which are the
// first to go on a narrow pane.
func (a *app) statusParts() []statusPart {
	var mode string
	switch {
	case a.grabbed:
	case a.inZellij:
		// handleInput drops typing while ungrabbed under Zellij; F1-F11,
		// Alt+letters and Esc still reach the device.
		mode = "typing goes to Zellij · F12 gives control"
	default:
		mode = "mouse free (F12)"
	}
	var kb string
	if a.kb != nil && a.kb.open {
		kb = "[KB]"
	}
	var vol string
	if a.cfg.audio {
		vol = "audio off"
		if a.audio.err == nil && !a.audioOff.Load() {
			vol = "vol " + strconv.Itoa(a.audio.gainPercent()) + "%"
		}
	}
	var stats []string
	if a.frameW > 0 && a.frameH > 0 {
		stats = append(stats, fmt.Sprintf("%dx%d", a.frameW, a.frameH))
	}
	if a.ftCnt > 0 {
		avg := 0.0
		n := min(a.ftCnt, 32)
		for i := 0; i < n; i++ {
			idx := (a.ftIdx - 1 - i + len(a.ftHist)) % len(a.ftHist)
			avg += a.ftHist[idx]
		}
		avg /= float64(n)
		fps := 0.0
		if avg > 0.01 {
			fps = 1000.0 / avg
		}
		stats = append(stats, fmt.Sprintf("%4.1fms %4.1ffps", avg, fps))
	}
	return []statusPart{
		{text: mode, shrink: true},
		{text: kb},
		{text: vol},
		{text: a.knownDeviceName(), shrink: true},
		{text: strings.Join(stats, " ")},
		{text: a.sparkline(20), join: " "},
	}
}

// fitStatus lays the status row out in width cells: the hints, then " | " and
// the parts while they fit. The first part that does not fit ends the row, so
// the least important ones are the first to go.
func fitStatus(width int, parts []statusPart) string {
	used := cellWidth(statusHints)
	if used > width {
		return truncateCells(statusQuitHint, width)
	}
	var b strings.Builder
	b.WriteString(statusHints)
	first := true
	for _, p := range parts {
		if p.text == "" {
			continue
		}
		join := " · "
		switch {
		case first:
			join = " | "
		case p.join != "":
			join = p.join
		}
		room := width - used - cellWidth(join)
		w := cellWidth(p.text)
		if w > room {
			if p.shrink && room >= statusMinShrink {
				b.WriteString(join + ellipsize(p.text, room))
			}
			break
		}
		b.WriteString(join + p.text)
		used += cellWidth(join) + w
		first = false
	}
	return b.String()
}

func (a *app) shutdown() {
	if a.audio != nil {
		a.audio.close()
	}
}
