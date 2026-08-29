package main

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
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

	// palette state
	paletteOpen bool
	paletteIdx  int
	paletteKeys []int // sorted keycode values

	mouseDown bool

	// frame geometry for pointer mapping
	frameW, frameH int
}

type inputEvent struct {
	kind int // evBytes, evResize, evQuit, evTick
	buf  []byte
}

const (
	evBytes  = 0
	evResize = 1
	evQuit   = 2
	evTick   = 3
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
	// palette list: all keycodes sorted
	for k := range androidKeycodes {
		a.paletteKeys = append(a.paletteKeys, k)
	}
	sort.Ints(a.paletteKeys)

	if !cfg.noTUI {
		a.tui = newTUI()
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
		fmt.Fprintf(stderrWriter(), "sct: audio disabled: %v\n", a.audio.err)
	}

	a.tui.shellInit()
	defer a.tui.shellClose()

	a.setMouse(a.grabbed)
	a.tui.setStatus(a.statusLine())

	go a.inputLoop()
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM)
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
			fmt.Fprintf(stderrWriter(), "sct: video: %v\n", err)
			a.events <- inputEvent{kind: evQuit}
		}
	}()
	go func() {
		if err := a.stream.runAudio(a.audio); err != nil {
			fmt.Fprintf(stderrWriter(), "sct: audio: %v\n", err)
		}
	}()

	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			a.events <- inputEvent{kind: evTick}
		}
	}()

	for {
		select {
		case ev := <-a.events:
			switch ev.kind {
			case evQuit:
				return nil
			case evResize:
				a.tui.resize()
				a.tui.setStatus(a.statusLine())
			case evTick:
				a.tui.setStatus(a.statusLine())
			case evBytes:
				a.handleInput(ev.buf)
			}
		case f := <-a.stream.deliver:
			a.frameW, a.frameH = f.w, f.h
			a.tui.draw(f.rgb)
		}
	}
}

func (a *app) runHeadless() error {
	go func() {
		if err := a.stream.runVideo(); err != nil {
			fmt.Fprintf(stderrWriter(), "sct: video: %v\n", err)
			a.events <- inputEvent{kind: evQuit}
		}
	}()
	go func() {
		if err := a.stream.runAudio(a.audio); err != nil {
			fmt.Fprintf(stderrWriter(), "sct: audio: %v\n", err)
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
			fmt.Fprintf(os.Stdout, "\r[%dx%d fps=%.0f audio=%s pcm=%dKB]   ",
				a.frameW, a.frameH, a.stream.currentFPS, audioState(a),
				a.stream.audioBytes/1024)
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

func (a *app) statusLine() string {
	if a.tui == nil || a.audio == nil {
		return ""
	}
	if a.paletteOpen {
		return fmt.Sprintf("sct %s · key palette · Ctrl-K/Esc close · Enter send", a.sess.deviceName)
	}
	vol := "-"
	if a.audio != nil && a.cfg.audio && a.audio.err == nil {
		vol = strconv.Itoa(a.audio.gainPercent()) + "%"
	}
	g := "grab"
	if !a.grabbed {
		g = "UNGRAB"
	}
	name := "device"
	if a.sess != nil {
		name = a.sess.deviceName
	}
	return fmt.Sprintf("sct %s  %dx%d  %s  vol %s  | Esc back · F1 home · F2 menu · F3 recents · F5/F6 dev-vol · F7 mute · F8 rotate · F9/F10 shade · Ctrl-K keys · Alt+M local-mute · Ctrl-Q quit",
		name, a.frameW, a.frameH, g, vol)
}

func (a *app) shutdown() {
	if a.audio != nil {
		a.audio.close()
	}
}
