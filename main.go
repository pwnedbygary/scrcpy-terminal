package main

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed third_party/scrcpy-server.jar.d/scrcpy-server
var embeddedServer []byte

// version is stamped at build time: -ldflags "-X main.version=v1.0.0".
var version = "dev"

func serverJarData() []byte { return embeddedServer }

func stderrWriter() *os.File { return os.Stderr }

type config struct {
	serial          string
	video           bool
	audio           bool
	control         bool
	videoBitRate    int
	audioBitRate    int
	maxSize         int
	maxFps          float64
	noTUI           bool // headless test mode (dump stats only)
	keys            bool
	dumpFrames      string // dir to dump first frames as PPM (verification)
	audioDump       string // file to dump raw opus wire packets (verification)
	audioDup        bool   // keep audio playing on the device while capturing
	audioSource     string // device audio capture source: output (default) | playback | mic
	audioCodec      string // device audio codec: opus (default) | aac | flac | raw
	repaintInterval int    // forced full redraw cadence in frames (default 300 ≈ 5s at 60fps)

	// web / window display mode
	web        bool   // serve a browser-based mirror instead of the TUI
	window     bool   // --web plus: pop the mirror out into its own window
	webPort    int    // TCP port for the web display (0 = pick a free one)
	webAddr    string // interface to bind (default 127.0.0.1)
	webQuality int    // mjpeg quality 2..31, lower = better (default 5)
	windowSize string // WxH for --window (default: sized to the device)

	// peer mode: a paired scterm device (the Android app) over the network
	usePeer   bool   // --peer was given, even empty
	peerQuery string // its name or identity; "" = the only paired device
	takeover  bool   // take control on connect even if another device has it
}

func main() {
	if runPeerCommand(os.Args[1:]) {
		return
	}
	cfg := config{
		video:           true,
		audio:           true,
		control:         true,
		videoBitRate:    8000000,
		audioBitRate:    128000,
		maxSize:         1280,
		repaintInterval: 300,
		webAddr:         "127.0.0.1",
		webPort:         6969,
		webQuality:      5,
	}
	var mirrorFPS float64
	parseFlags(&cfg, &mirrorFPS)
	if cfg.keys {
		printSortedKeys()
		return
	}

	// --web/--window replace the terminal UI: the browser is the display, so
	// the terminal must stay usable (no alternate screen, no raw mode, no
	// mouse grab). --no-tui still means "no display at all".
	if cfg.window {
		cfg.web = true
	}
	if cfg.web && cfg.noTUI {
		fatal(fmt.Errorf("--web/--window and --no-tui are mutually exclusive"))
	}

	// Show the version in the status line (useful for bug reports).
	logOnce(fmt.Sprintf("scterm %s\n", version))

	var sess *session
	if cfg.usePeer {
		s, err := newPeerSession(cfg.peerQuery, cfg)
		if err != nil {
			fatal(err)
		}
		sess = s
	} else {
		serial, err := findDevice(cfg.serial)
		if err != nil {
			fatal(err)
		}
		s, err := newSession(serial)
		if err != nil {
			fatal(err)
		}
		if err := s.start(cfg.video, cfg.audio, cfg.control,
			serverParams(cfg.video, cfg.audio, cfg.control, cfg)); err != nil {
			fatal(fmt.Errorf("start: %w", err))
		}
		sess = s
	}
	defer sess.stop()

	// Remove any sink-inputs left behind by a previous hard-killed sct
	// (they keep playing audio from a dead process).
	cleanupStaleStreams()

	app := newApp(sess, cfg)
	err := app.run()
	app.shutdown()
	// A peer target says why it ended a session (revoked, serving stopped, ...).
	if sess.peer != nil {
		if reason := sess.peer.Reason(); reason != "" && reason != "closed" {
			fmt.Fprintf(os.Stderr, "scterm: %s ended the session: %s\n", sess.deviceName, reason)
		}
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "scterm: %v\n", err)
	os.Exit(1)
}
