package main

import (
	_ "embed"
	"fmt"
	"os"
	"time"
)

//go:embed third_party/scrcpy-server.jar.d/scrcpy-server
var embeddedServer []byte

func serverJarData() []byte { return embeddedServer }

func stderrWriter() *os.File { return os.Stderr }

type config struct {
	serial       string
	video        bool
	audio        bool
	control      bool
	videoBitRate int
	audioBitRate int
	maxSize      int
	maxFps       float64
	noTUI        bool // headless test mode (dump stats only)
	keys         bool
	dumpFrames   string // dir to dump first frames as PPM (verification)
}

func main() {
	cfg := config{
		video:        true,
		audio:        true,
		control:      true,
		videoBitRate: 8000000,
		audioBitRate: 128000,
		maxSize:      1280,
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

	sess, err := newSession(serial)
	if err != nil {
		fatal(err)
	}
	if err := sess.start(cfg.video, cfg.audio, cfg.control,
		serverParams(cfg.video, cfg.audio, cfg.control, cfg)); err != nil {
		fatal(fmt.Errorf("start: %w", err))
	}
	defer sess.stop()

	app := newApp(sess, cfg)
	app.stream = newStreamState(sess, cfg, app.ctrl)
	if err := app.run(); err != nil {
		app.shutdown()
		fatal(err)
	}
	app.shutdown()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "sct: %v\n", err)
	os.Exit(1)
}

var _ = time.Second
