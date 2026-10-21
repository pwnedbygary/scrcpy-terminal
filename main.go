package main

import (
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
)

//go:embed third_party/scrcpy-server.jar.d/scrcpy-server
var embeddedServer []byte

// version is stamped at build time: -ldflags "-X main.version=2.1.0" (the
// release tag without its "v"). Source builds stay "dev".
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

// defaultConfig is the configuration flags start from.
func defaultConfig() config {
	return config{
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
}

func main() {
	if runPeerCommand(os.Args[1:]) || runUpdateCommand(os.Args[1:]) || runDoctorCommand(os.Args[1:]) {
		return
	}
	cfg := defaultConfig()
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
	if err := validateConfig(cfg); err != nil {
		usageError(err.Error())
	}

	// A session prints its version first, on stderr, as scterm version prints
	// it (useful for bug reports).
	logOnce(fmt.Sprintf("scterm %s\n", version))

	// The browser display listens before any device or peer is touched, so
	// that a port it cannot use ends the run at once; the display then serves
	// this listener.
	var webLn net.Listener
	if cfg.web {
		ln, err := listenWeb(cfg.webAddr, cfg.webPort)
		if err != nil {
			fatal(err)
		}
		webLn = ln
	}

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
	if cfg.web {
		app.web.ln = webLn
	}
	err := app.run()
	app.shutdown()
	endSession(sess, err)
}

// endSession reports how a session ended. A peer target's reason comes first;
// an error then stops the session and exits with the error's line and status,
// while a quit returns for main to return.
func endSession(sess *session, err error) {
	// A peer target says why it ended a session (revoked, serving stopped, ...).
	if sess.peer != nil {
		if reason := sess.peer.Reason(); reason != "" && reason != "closed" {
			fmt.Fprintf(os.Stderr, "scterm: %s ended the session: %s\n", sess.deviceName, reason)
		}
	}
	if err != nil {
		sess.stop() // exitWith's exit skips main's deferred stop
		exitWith(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "scterm: %v\n", err)
	os.Exit(1)
}

// exitWith reports the error a session ended with and exits with its status.
func exitWith(err error) {
	fmt.Fprintf(os.Stderr, "scterm: %v\n", err)
	os.Exit(exitStatus(err))
}

// exitStatus is the exit status of a session that ended with err, as scrcpy's:
// 0 for a quit, 2 when the device was lost during the session (the stream the
// display shows ended after it started), and 1 for anything else.
func exitStatus(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.As(err, new(lostError)):
		return 2
	}
	return 1
}
