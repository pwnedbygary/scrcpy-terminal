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
	repaintInterval int    // forced full redraw cadence in frames (default 300 ≈ 5s at 60fps)
}

func main() {
	cfg := config{
		video:           true,
		audio:           true,
		control:         true,
		videoBitRate:    8000000,
		audioBitRate:    128000,
		maxSize:         1280,
		repaintInterval: 300,
	}
	var mirrorFPS float64
	parseFlags(&cfg, &mirrorFPS)
	if cfg.keys {
		printSortedKeys()
		return
	}

	serial, err := findDevice(cfg.serial)
	if err != nil {
		fatal(err)
	}

	// Show the version in the status line (useful for bug reports).
	logOnce(fmt.Sprintf("scterm %s\n", version))

	sess, err := newSession(serial)
	if err != nil {
		fatal(err)
	}
	if err := sess.start(cfg.video, cfg.audio, cfg.control,
		serverParams(cfg.video, cfg.audio, cfg.control, cfg)); err != nil {
		fatal(fmt.Errorf("start: %w", err))
	}
	defer sess.stop()

	// Remove any sink-inputs left behind by a previous hard-killed sct
	// (they keep playing audio from a dead process).
	cleanupStaleStreams()

	app := newApp(sess, cfg)
	app.stream = newStreamState(sess, cfg, app.ctrl)
	if err := app.run(); err != nil {
		app.shutdown()
		fatal(err)
	}
	app.shutdown()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "scterm: %v\n", err)
	os.Exit(1)
}
