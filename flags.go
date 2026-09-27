package main

import (
	"flag"
	"fmt"
	"os"
)

func parseFlags(cfg *config, mirrorFPS *float64) {
	fs := flag.NewFlagSet("scterm", flag.ExitOnError)
	fs.StringVar(&cfg.serial, "s", "", "device serial (default: ANDROID_SERIAL or the only device)")
	fs.IntVar(&cfg.maxSize, "max-size", cfg.maxSize, "maximum video size (0 = device size)")
	fs.IntVar(&cfg.videoBitRate, "video-bit-rate", cfg.videoBitRate, "video bit rate")
	fs.IntVar(&cfg.audioBitRate, "audio-bit-rate", cfg.audioBitRate, "audio bit rate")
	fs.Float64Var(&cfg.maxFps, "max-fps", cfg.maxFps, "maximum fps (0 = device default)")
	fs.BoolVar(&cfg.video, "video", cfg.video, "enable video")
	fs.BoolVar(&cfg.audio, "audio", cfg.audio, "enable audio")
	fs.BoolVar(&cfg.control, "control", cfg.control, "enable control")
	fs.BoolVar(&cfg.noTUI, "no-tui", cfg.noTUI, "run without the TUI (headless stats)")
	fs.BoolVar(&cfg.keys, "keys", cfg.keys, "print all supported Android keys and exit")
	fs.StringVar(&cfg.dumpFrames, "dump-frames", "", "dump first frames as PPM to this dir (verification)")
	fs.StringVar(&cfg.audioDump, "dump-audio", "", "dump raw audio wire packets to this file (verification)")
	fs.BoolVar(&cfg.audioDup, "audio-dup", cfg.audioDup, "duplicate the audio: keep it playing on the device while capturing, and (with --web/--window) play it on the host as well as in the browser")
	fs.StringVar(&cfg.audioSource, "audio-source", cfg.audioSource, "device audio capture source: output (default, remote submix), playback (Android 13+, low latency, enables audio-dup), mic")
	fs.StringVar(&cfg.audioCodec, "audio-codec", cfg.audioCodec, "device audio codec: opus (default) | aac | flac | raw (uncompressed PCI, skips device encoder)")
	fs.Float64Var(mirrorFPS, "mirror-fps", 0, "cap display framerate (0 = uncapped)")
	fs.IntVar(&cfg.repaintInterval, "repaint-interval", cfg.repaintInterval, "forced full-redraw cadence in frames (desync-recovery safety net; default 300 ≈ 5s at 60fps)")

	// ---- web / window display -------------------------------------------
	fs.BoolVar(&cfg.web, "web", cfg.web, "serve the mirror over HTTP to a browser (canvas display, same controls)")
	fs.BoolVar(&cfg.window, "window", cfg.window, "like --web, but pop the mirror out into its own window")
	fs.IntVar(&cfg.webPort, "web-port", cfg.webPort, "TCP port for --web/--window (0 = pick a free port)")
	fs.StringVar(&cfg.webAddr, "web-addr", cfg.webAddr, "interface to bind for --web/--window (default 127.0.0.1; 0.0.0.0 exposes it to your network)")
	fs.IntVar(&cfg.webQuality, "web-quality", cfg.webQuality, "mjpeg quality for the web display: 2 (best, big) .. 31 (small)")
	fs.StringVar(&cfg.windowSize, "window-size", cfg.windowSize, "window size for --window as WxH (default: fitted to the device)")

	// ---- peer mode (the scterm Android app, no adb) -----------------------
	fs.StringVar(&cfg.peerQuery, "peer", "", "connect to a paired scterm device over the network instead of adb: its name or identity (--peer= for the only paired device; see 'scterm peers')")
	fs.BoolVar(&cfg.takeover, "takeover", cfg.takeover, "with --peer: take control even if another device has it")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "scterm - scrcpy in your terminal\n\n")
		fmt.Fprintf(os.Stderr, "usage: scterm [flags]\n")
		fmt.Fprintf(os.Stderr, "       scterm pair \"HOST:PORT CODE\"   pair with an scterm device from its invitation\n")
		fmt.Fprintf(os.Stderr, "       scterm peers                   list paired devices\n")
		fmt.Fprintf(os.Stderr, "       scterm forget NAME             forget a paired device\n\nflags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "peer" {
			cfg.usePeer = true
		}
	})
}
