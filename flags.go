package main

import (
	"flag"
	"fmt"
	"os"
)

func parseFlags(cfg *config, mirrorFPS *float64) {
	fs := flag.NewFlagSet("sct", flag.ExitOnError)
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
	fs.Float64Var(mirrorFPS, "mirror-fps", 0, "cap display framerate (0 = uncapped)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "sct - scrcpy in your terminal\n\n")
		fmt.Fprintf(os.Stderr, "usage: sct [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])
}
