package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// newFlagSet defines scterm's flags, writing into cfg.
func newFlagSet(cfg *config, mirrorFPS *float64) *flag.FlagSet {
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
	fs.StringVar(&cfg.peerQuery, "peer", "", "connect to a paired scterm device over the network instead of adb: its name, the start of its name or a word in it, or its identity (--peer= for the only paired device, or to choose one; see 'scterm peers')")
	fs.BoolVar(&cfg.takeover, "takeover", cfg.takeover, "with --peer: take control even if another device has it")
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "scterm - scrcpy in your terminal\n\n")
		fmt.Fprintf(w, "usage: scterm [flags]\n")
		fmt.Fprintf(w, "       scterm pair                    pair with a device inviting on this network, by comparing a code\n")
		fmt.Fprintf(w, "       scterm pair \"HOST:PORT CODE\"   pair with an scterm device from its invitation\n")
		fmt.Fprintf(w, "       scterm peers [--check]         list paired devices, optionally check readiness\n")
		fmt.Fprintf(w, "       scterm forget NAME             forget a paired device\n")
		fmt.Fprintf(w, "       scterm update                  update to the latest release (or pull and rebuild a source checkout)\n")
		fmt.Fprintf(w, "       scterm version                 print the version\n")
		fmt.Fprintf(w, "       scterm help                    print this help\n\nflags:\n")
		fs.PrintDefaults()
	}
	return fs
}

func parseFlags(cfg *config, mirrorFPS *float64) {
	fs := newFlagSet(cfg, mirrorFPS)
	if len(os.Args) > 1 && os.Args[1] == "help" {
		fs.SetOutput(os.Stdout)
		fs.Usage()
		os.Exit(0)
	}
	fs.Parse(os.Args[1:])
	if fs.NArg() > 0 {
		usageError(leftoverWordsError(fs, os.Args[1:], fs.Args()))
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "peer" {
			cfg.usePeer = true
		}
	})
}

// commands are the words scterm takes instead of flags, as its first argument.
var commands = []string{"pair", "peers", "forget", "update", "version", "help"}

// spacedValueFlags take values that may contain spaces (names, paths), so a
// word left over after one is most likely the rest of an unquoted value.
var spacedValueFlags = map[string]bool{"peer": true, "dump-frames": true, "dump-audio": true}

// leftoverWordsError explains words left over after the flags. Without it
// `scterm help` or a mistyped command would start mirroring the device. args
// are all the arguments, so a word can be explained by the flag before it.
func leftoverWordsError(fs *flag.FlagSet, args, words []string) string {
	w := words[0]
	for _, c := range commands {
		if w == c {
			return fmt.Sprintf("%q is a command, not a flag value: run it on its own, as scterm %s", w, w)
		}
	}
	if i := len(args) - len(words); i > 0 {
		prev := args[i-1]
		if f := lookupFlag(fs, prev); f != nil && isBoolFlag(f) && !strings.Contains(prev, "=") {
			if _, err := strconv.ParseBool(w); err == nil {
				return fmt.Sprintf("%s takes no separate value: write %s=%s", prev, prev, w)
			}
		}
		if i > 1 && !strings.Contains(args[i-2], "=") {
			if f := lookupFlag(fs, args[i-2]); f != nil && spacedValueFlags[f.Name] {
				value := strings.Join(append([]string{prev}, words...), " ")
				return fmt.Sprintf("unexpected %q after %s %s: quote a value with spaces, as %s %q", w, args[i-2], prev, args[i-2], value)
			}
		}
	}
	hint := "; run scterm -h for the commands and flags"
	switch c := closestCommand(w); {
	case w == "peer":
		return fmt.Sprintf("unknown command %q (did you mean peers, or --peer NAME?)%s", w, hint)
	case c != "":
		return fmt.Sprintf("unknown command %q (did you mean %s?)%s", w, c, hint)
	}
	return fmt.Sprintf("unknown command %q%s", w, hint)
}

// lookupFlag returns the flag an argument such as -s, --peer or --audio=false
// names, or nil when the argument is not a flag.
func lookupFlag(fs *flag.FlagSet, arg string) *flag.Flag {
	if !strings.HasPrefix(arg, "-") {
		return nil
	}
	name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	return fs.Lookup(name)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// closestCommand is the command w is most likely a typo of, or "".
func closestCommand(w string) string {
	best, bestDist := "", 3
	for _, c := range commands {
		if len(w) >= 3 && strings.HasPrefix(c, w) {
			return c
		}
		if d := editDistance(w, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between two short ASCII words.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// usageError reports a mistake in how scterm was run and exits with status 2,
// as the flag package does for an unknown flag.
func usageError(msg string) {
	fmt.Fprintf(os.Stderr, "scterm: %s\n", msg)
	os.Exit(2)
}

// The values the bundled scrcpy server accepts (AudioCodec.java and
// AudioSource.java). It refuses anything else before it connects, so they are
// checked here, where the mistake can be named.
var (
	audioCodecs  = []string{"opus", "aac", "flac", "raw"}
	audioSources = []string{"output", "playback", "mic", "mic-unprocessed", "mic-camcorder",
		"mic-voice-recognition", "mic-voice-communication", "voice-call",
		"voice-call-uplink", "voice-call-downlink", "voice-performance"}
)

// validateConfig rejects flag combinations that cannot work, before any
// device is touched.
func validateConfig(cfg config) error {
	if !cfg.video && !cfg.audio && !cfg.control {
		return errors.New("--video, --audio and --control are all off, so there is nothing to show or control")
	}
	if cfg.audioCodec != "" && !slices.Contains(audioCodecs, cfg.audioCodec) {
		return fmt.Errorf("--audio-codec %q is not one of %s", cfg.audioCodec, strings.Join(audioCodecs, ", "))
	}
	if cfg.audioSource != "" && !slices.Contains(audioSources, cfg.audioSource) {
		return fmt.Errorf("--audio-source %q is not one of %s", cfg.audioSource, strings.Join(audioSources, ", "))
	}
	return nil
}
