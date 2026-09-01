package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
	grabbed  bool
	inZellij bool

	// input events channel
	events chan inputEvent

	// software keyboard
	kb *keyboard

	mouseDown bool

	// frame geometry for pointer mapping
	frameW, frameH int

	// frametime history for the status-bar sparkline (latest last)
	ftHist [96]float64 // ms per frame
	ftIdx  int
	ftCnt  int

	lastFrameNano int64

	// coalesced drag move state
	lastMoveNano int64
	pendingMove  position

	// keyboard auto-grabbed the mouse on open (Zellij); restore on close
	kbAutoGrabbed bool
}

type inputEvent struct {
	kind int // evBytes, evResize, evQuit, evTick
	buf  []byte
}

const (
	evBytes    = 0
	evResize   = 1
	evQuit     = 2
	evTick     = 3
	evTickFast = 4 // ~16ms: flushes coalesced drag moves without waiting for evTick
)

func newApp(sess *session, cfg config) *app {
	a := &app{
		sess:    sess,
		cfg:     cfg,
		audio:   newAudioSink(),
		events:  make(chan inputEvent, 256),
		grabbed: true,
	}
	_, a.inZellij = os.LookupEnv("ZELLIJ")
	if a.inZellij {
		a.grabbed = false
	}
	if cfg.control && sess.control != nil {
		a.ctrl = newController(sess.control)
		go deviceMsgReader(sess.control)
	}
	if !cfg.noTUI {
		a.tui = newTUI()
		a.tui.repaintInterval = cfg.repaintInterval
		a.kb = newKeyboard()
	}
	return a
}

func (a *app) run() error {
	if a.tui == nil {
		return a.runHeadless()
	}

	oldMode, err := setRawMode(ttyReadFD())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer restoreMode(ttyReadFD(), oldMode)

	// Print warnings before entering the alternate screen so they don't
	// corrupt the TUI.
	if a.cfg.audio && a.audio.err != nil {
		fmt.Fprintf(stderrWriter(), "scterm: audio disabled: %v\n", a.audio.errString())
	}

	a.tui.shellInit()
	defer a.tui.shellClose()

	a.setMouse(a.grabbed)
	a.tui.setStatus(a.statusLine())

	go a.inputLoop()
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				a.events <- inputEvent{kind: evResize}
			default:
				a.events <- inputEvent{kind: evQuit}
			}
		}
	}()

	go func() {
		if err := a.stream.runVideo(); err != nil {
			fmt.Fprintf(stderrWriter(), "scterm: video: %v\n", err)
			a.events <- inputEvent{kind: evQuit}
		}
	}()
	go func() {
		if err := a.stream.runAudio(a.audio); err != nil {
			fmt.Fprintf(stderrWriter(), "scterm: audio: %v\n", err)
		}
	}()

	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			a.events <- inputEvent{kind: evTick}
		}
	}()
	go func() {
		fast := time.NewTicker(16 * time.Millisecond)
		defer fast.Stop()
		for range fast.C {
			a.events <- inputEvent{kind: evTickFast}
		}
	}()
	go a.followAudioSink()

	for {
		select {
		case ev := <-a.events:
			switch ev.kind {
			case evQuit:
				return nil
			case evResize:
				a.tui.resize()
				a.stream.markGeometryDirty()
				a.tui.setStatus(a.statusLine())
			case evTickFast:
				a.flushPendingMove()
			case evTick:
				a.flushPendingMove()
				a.tui.setStatus(a.statusLine())
			case evBytes:
				a.handleInput(ev.buf)
			}
		case f := <-a.stream.deliver:
			now := timeNowUnixNano()
			if a.lastFrameNano != 0 {
				a.recordFrameTime(float64(now-a.lastFrameNano) / 1e6)
			}
			a.lastFrameNano = now
			a.frameW, a.frameH = f.w, f.h
			a.tui.draw(f.rgb)
			a.stream.returnPooled(f.rgb)
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
	go func() {
		if err := a.stream.runVideo(); err != nil {
			fmt.Fprintf(stderrWriter(), "scterm: video: %v\n", err)
			a.events <- inputEvent{kind: evQuit}
		}
	}()
	go func() {
		if err := a.stream.runAudio(a.audio); err != nil {
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
		case f := <-a.stream.deliver:
			a.frameW, a.frameH = f.w, f.h
			if a.cfg.dumpFrames != "" {
				if dumped < 3 {
					dumpPPM(a.cfg.dumpFrames, dumped, f.rgb, f.cw, f.ch)
					dumped++
					fmt.Fprintf(os.Stdout, "dumped frame %d: %dx%d (canvas %dx%d)\n", dumped, f.w, f.h, f.cw, f.ch)
				}
			}
		case <-ticker.C:
			fmt.Fprintf(os.Stdout, "\r[%dx%d fps=%.0f audio=%s pcm=%dKB peak=%d]   ",
				a.frameW, a.frameH, a.stream.currentFPS, audioState(a),
				a.stream.audioBytes/1024, a.stream.audioPeak)
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

// setMouse enables/disables terminal mouse tracking.
func (a *app) setMouse(on bool) {
	if on {
		os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[?1015h")
	} else {
		os.Stdout.WriteString("\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[?1015l")
	}
	a.grabbed = on
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
	// frontend. Re-assert grab briefly after 300ms as a handshake nudge.
	if a.inZellij {
		time.AfterFunc(300*time.Millisecond, func() {
			if a.kb == nil || !a.kb.open {
				return
			}
			if !a.grabbed {
				a.setMouse(true)
				a.kbAutoGrabbed = true
				a.refreshKeyboard()
			}
		})
	}
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
		a.tui.setOverlay(nil)
		a.tui.setStatus(a.statusLine())
		a.tui.markDirty()
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
	if a.kb.open {
		_, rows := termSize()
		a.tui.setOverlay(a.kb.lines(rows))
	} else {
		a.tui.setOverlay(nil)
	}
	a.tui.setStatus(a.statusLine())
	a.tui.markDirty()
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
	blocks := []rune("  ▁▂▃▄▅▆▇█")
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		v := a.ftHist[(start+i)%len(a.ftHist)]
		if v > maxMs {
			v = maxMs
		}
		// 0..100ms -> 1..7 (lowest bar = after the leading space)
		h := int(v / maxMs * 7.0)
		if h < 1 {
			h = 1
		}
		if h > 7 {
			h = 7
		}
		out = append(out, blocks[h])
	}
	return string(out)
}

func (a *app) statusLine() string {
	if a.tui == nil || a.audio == nil {
		return ""
	}
	vol := "-"
	if a.audio != nil && a.cfg.audio {
		if a.audio.err == nil {
			vol = strconv.Itoa(a.audio.gainPercent()) + "%"
		} else {
			vol = "off"
		}
	}
	g := "grab"
	if !a.grabbed {
		g = "UNGRAB"
	}
	name := "device"
	if a.sess != nil {
		name = a.sess.deviceName
	}
	kb := ""
	if a.kb != nil && a.kb.open {
		kb = "  [KB]"
	}
	var ft string
	if a.ftCnt > 0 {
		avg := 0.0
		n := a.ftCnt
		if n > 32 {
			n = 32
		}
		for i := 0; i < n; i++ {
			idx := (a.ftIdx - 1 - i + len(a.ftHist)) % len(a.ftHist)
			avg += a.ftHist[idx]
		}
		avg /= float64(n)
		fps := 0.0
		if avg > 0.01 {
			fps = 1000.0 / avg
		}
		ft = fmt.Sprintf("  %s %4.1fms %4.1ffps ", a.sparkline(20), avg, fps)
	}
	return fmt.Sprintf("scterm %s %dx%d %s vol %s%s%s| Esc back · F1-F4 home/menu/recents/power · F5/F6 dev-vol · F7 mute · F8 rotate · F9/F10 shade · Ctrl-K kb · Alt+M mute · Alt+S shot · Alt+K reset video · Alt+Q quit",
		name, a.frameW, a.frameH, g, vol, kb, ft)
}

func (a *app) shutdown() {
	if a.audio != nil {
		a.audio.close()
	}
}
