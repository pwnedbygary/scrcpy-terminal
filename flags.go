package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
)

// newFlagSet defines scterm's flags, writing into cfg. Their usage strings
// are what scterm -h says about them, arranged by helpSections.
func newFlagSet(cfg *config, mirrorFPS *float64) *flag.FlagSet {
	fs := flag.NewFlagSet("scterm", flag.ContinueOnError)
	// parseArgs words a mistake in one line; the flag package would print the
	// whole help after it.
	fs.SetOutput(io.Discard)
	fs.Usage = func() { printHelp(fs.Output(), fs) }

	// ---- device ------------------------------------------------------------
	fs.StringVar(&cfg.serial, "s", "", "the device to use, as adb devices lists it (default: $ANDROID_SERIAL, or the only one ready)")
	alias(fs, "serial", "s")
	// scterm -h shows scrcpy's --no-control, --no-video and --no-audio
	// rather than these, which are on by default.
	fs.BoolVar(&cfg.control, "control", cfg.control, "enable control")
	fs.Var((*negatedBool)(&cfg.control), "no-control", "view only: send no input (same as --control=false)")
	alias(fs, "n", "no-control")

	// ---- video -------------------------------------------------------------
	fs.BoolVar(&cfg.video, "video", cfg.video, "enable video")
	fs.Var((*negatedBool)(&cfg.video), "no-video", "mirror without the picture (same as --video=false)")
	fs.IntVar(&cfg.maxSize, "max-size", cfg.maxSize, "cap the picture's longer side, in pixels; 0 keeps the device's full size")
	alias(fs, "m", "max-size")
	fs.Float64Var(&cfg.maxFps, "max-fps", cfg.maxFps, "cap the frames per second (default: no cap)")
	fs.Var((*bitRate)(&cfg.videoBitRate), "video-bit-rate", "video bit rate in bits per second; K means thousands and M millions, as in 1.5M")
	alias(fs, "b", "video-bit-rate")

	// ---- audio -------------------------------------------------------------
	fs.BoolVar(&cfg.audio, "audio", cfg.audio, "enable audio")
	fs.Var((*negatedBool)(&cfg.audio), "no-audio", "mirror without sound (same as --audio=false)")
	fs.Var((*bitRate)(&cfg.audioBitRate), "audio-bit-rate", "audio bit rate in bits per second")
	fs.StringVar(&cfg.audioCodec, "audio-codec", cfg.audioCodec, "the device's audio codec: opus (the default), aac, flac or raw (uncompressed PCM)")
	fs.StringVar(&cfg.audioSource, "audio-source", cfg.audioSource, "what the device records: output (the default; mutes the device), playback (Android 13+), mic, or another scrcpy source such as voice-call")
	fs.BoolVar(&cfg.audioDup, "audio-dup", cfg.audioDup, "keep playing the sound on the device (implies --audio-source playback, Android 13+); with --web or --window, on this computer too")

	// ---- display: terminal, browser or window ------------------------------
	fs.BoolVar(&cfg.noTUI, "no-tui", cfg.noTUI, "no picture, only a line of statistics (for tests)")
	fs.BoolVar(&cfg.web, "web", cfg.web, "show the mirror in a web browser instead of this terminal, at the address scterm prints")
	fs.BoolVar(&cfg.window, "window", cfg.window, "like --web, in its own Chromium or Firefox window")
	fs.IntVar(&cfg.webPort, "web-port", cfg.webPort, "the TCP port to serve on; 0 picks a free one")
	fs.StringVar(&cfg.webAddr, "web-addr", cfg.webAddr, "the address to listen on; 0.0.0.0 lets anyone on your network connect")
	fs.IntVar(&cfg.webQuality, "web-quality", cfg.webQuality, "JPEG quality of the picture in the browser: 2 is best and largest, 31 smallest")
	fs.StringVar(&cfg.windowSize, "window-size", cfg.windowSize, "the size of the window --window opens, in pixels, as 720x1600 (default: fitted to the picture)")

	// ---- peer mode (the scterm Android app, no adb) ------------------------
	fs.StringVar(&cfg.peerQuery, "peer", "", "connect to a device running the scterm app, over the network instead of adb. NAME is its name, the start of a word in it, or its identity; --peer= takes the only paired device, or asks which")
	fs.BoolVar(&cfg.takeover, "takeover", cfg.takeover, "with --peer, take control from whoever has it")

	// ---- debugging ---------------------------------------------------------
	fs.BoolVar(&cfg.keys, "keys", cfg.keys, "print the Android keys scterm can send, and exit")
	fs.StringVar(&cfg.dumpFrames, "dump-frames", "", "with --no-tui, save the first 3 frames in DIR (PPM)")
	fs.StringVar(&cfg.audioDump, "dump-audio", "", "write the audio packets from the device to FILE, each after its 4-byte size")
	fs.IntVar(&cfg.repaintInterval, "repaint-interval", cfg.repaintInterval, "redraw the whole terminal every FRAMES frames, to clear any garbling")
	// Never used; accepted so that old command lines still run.
	fs.Float64Var(mirrorFPS, "mirror-fps", 0, "does nothing")
	return fs
}

// alias makes name another name for the flag of.
func alias(fs *flag.FlagSet, name, of string) {
	f := fs.Lookup(of)
	fs.Var(f.Value, name, f.Usage)
}

// negatedBool is a --no-NAME flag: it sets NAME's bool to the opposite.
type negatedBool bool

func (b *negatedBool) String() string   { return strconv.FormatBool(!bool(*b)) }
func (b *negatedBool) IsBoolFlag() bool { return true }

func (b *negatedBool) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return errors.New("parse error") // the flag package's word for a bad bool
	}
	*b = negatedBool(!v)
	return nil
}

// bitRate is a bit rate in bits per second, written as scrcpy writes it: a
// whole number, or a number of thousands or millions with a K or M suffix
// (8M, 128K, 1.5M).
type bitRate int

func (b *bitRate) String() string {
	switch n := int(*b); {
	case n != 0 && n%1000000 == 0:
		return strconv.Itoa(n/1000000) + "M"
	case n != 0 && n%1000 == 0:
		return strconv.Itoa(n/1000) + "K"
	default:
		return strconv.Itoa(n)
	}
}

func (b *bitRate) Set(s string) error {
	places := 0 // how far the suffix moves the decimal point
	switch {
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "k"):
		s, places = s[:len(s)-1], 3
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "m"):
		s, places = s[:len(s)-1], 6
	}
	whole, frac, dot := strings.Cut(s, ".")
	if !isDigits(whole) || dot && !isDigits(frac) {
		return errors.New("want bits per second, as 8000000, 8M, 1.5M or 128K")
	}
	frac = strings.TrimRight(frac, "0")
	if len(frac) > places {
		return errors.New("want a whole number of bits per second")
	}
	// The scrcpy server reads a bit rate into a Java int.
	n, err := strconv.ParseInt(whole+frac+strings.Repeat("0", places-len(frac)), 10, 32)
	if err != nil {
		return fmt.Errorf("more than %d bits per second", math.MaxInt32)
	}
	*b = bitRate(n)
	return nil
}

func isDigits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// helpFlag is a flag's line in scterm -h: its names, with the aliases that
// share the line ("s serial"), and the word standing for its value, if any.
type helpFlag struct{ names, value string }

// helpSections lays out scterm -h. A flag's line shows its usage, then its
// default unless that is zero; other lines are shown as they are written.
// TestHelpListsEveryFlagOnce checks that it lists every flag but --mirror-fps.
var helpSections = []struct {
	heading string
	flags   []helpFlag
	lines   [][2]string // what to type, and what it does
}{
	{heading: "Device", flags: []helpFlag{{"s serial", "SERIAL"}, {"n no-control", ""}}},
	{heading: "Video", flags: []helpFlag{{"no-video", ""}, {"m max-size", "PIXELS"}, {"max-fps", "FPS"}, {"b video-bit-rate", "RATE"}}},
	{heading: "Audio", flags: []helpFlag{{"no-audio", ""}, {"audio-bit-rate", "RATE"}, {"audio-codec", "CODEC"},
		{"audio-source", "SOURCE"}, {"audio-dup", ""}}},
	{heading: "Display", flags: []helpFlag{{"no-tui", ""}, {"web", ""}, {"window", ""}, {"web-port", "PORT"},
		{"web-addr", "ADDRESS"}, {"web-quality", "N"}, {"window-size", "WxH"}}},
	{heading: "Peer", flags: []helpFlag{{"peer", "NAME"}, {"takeover", ""}}},
	{heading: "Debugging", flags: []helpFlag{{"keys", ""}, {"dump-frames", "DIR"}, {"dump-audio", "FILE"},
		{"repaint-interval", "FRAMES"}}, lines: [][2]string{
		{"SCT_DEBUG_WEB=1", "log each browser's paint and decode statistics"},
		{"SCT_DEBUG_CONTROL=1", "log input as it is read and sent to the device"},
	}},
	{heading: "Commands", lines: [][2]string{
		{"pair [INVITATION]", "pair with a device running the scterm app"},
		{"peers [--check]", "list the paired devices; --check tests each one"},
		{"forget NAME", "forget a paired device"},
		{"update", "update to the latest release, or pull and rebuild"},
		{"version", "print the version"},
		{"help", "print this help"},
		{"COMMAND -h", "print a command's own help"},
	}},
	{heading: "Environment", lines: [][2]string{
		{"ANDROID_SERIAL", "the device to use when -s is not given"},
		{"SCTERM_HOME", "where scterm keeps its identity and pairings (default: scterm in $XDG_CONFIG_HOME or ~/.config)"},
		{"SCTERM_CHROME", "the Chromium-family browser for --window (or CHROME_BIN, CHROMIUM_BIN)"},
		{"SCTERM_FIREFOX_KIOSK=1", "when --window falls back to Firefox, open it full screen without its toolbars"},
		{"SCT_AUDIO_DEVICE", "the PulseAudio sink to play the sound on, rather than following the desktop's choice"},
	}},
	{heading: "Exit status", lines: [][2]string{
		{"0", "a normal end: a quit, or a command that worked"},
		{"1", "scterm could not start (a mistake in the command line included), a command failed, or the session failed for another reason"},
		{"2", "the device was lost during the session (a crash exits 2 as well)"},
	}},
	{heading: "Examples", lines: [][2]string{
		{"scterm --window", "mirror the only device in a window of its own"},
		{"scterm -s R5CT --no-audio", "mirror one of several devices, without sound"},
		{"scterm -m 1920 -b 16M", "a sharper picture, for a faster connection"},
		{"scterm --peer pixel", "the paired device named Pixel, over the network"},
	}},
}

// helpColumn is where descriptions start in scterm -h, which fits 80 columns.
const helpColumn = 29

// printHelp writes scterm -h to w.
func printHelp(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, "scterm - scrcpy in your terminal\n\nusage: scterm [flags]\n       scterm COMMAND\n")
	for _, s := range helpSections {
		fmt.Fprintf(w, "\n%s:\n", s.heading)
		for _, h := range s.flags {
			names := strings.Fields(h.names)
			f := fs.Lookup(names[0])
			for i, n := range names {
				names[i] = "--" + n
				if len(n) == 1 {
					names[i] = "-" + n
				}
			}
			usage := f.Usage
			if d := f.DefValue; d != "" && d != "0" && d != "false" {
				usage += " (default " + d + ")"
			}
			printHelpLine(w, strings.TrimSpace(strings.Join(names, ", ")+" "+h.value), usage)
		}
		for _, l := range s.lines {
			printHelpLine(w, l[0], l[1])
		}
	}
}

// printHelpLine writes one entry of scterm -h: left, then text wrapped from
// helpColumn on. A left too wide for its column gets a line of its own.
func printHelpLine(w io.Writer, left, text string) {
	left = "  " + left
	if len(left)+2 > helpColumn {
		fmt.Fprintln(w, left)
		left = ""
	}
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case helpColumn+len(line)+1+len(word) <= 80:
			line += " " + word
		default:
			fmt.Fprintf(w, "%-*s%s\n", helpColumn, left, line)
			left, line = "", word
		}
	}
	fmt.Fprintf(w, "%-*s%s\n", helpColumn, left, line)
}

func parseFlags(cfg *config, mirrorFPS *float64) {
	fs := newFlagSet(cfg, mirrorFPS)
	switch err := parseArgs(fs, cfg, os.Args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		fs.SetOutput(os.Stdout)
		fs.Usage()
		os.Exit(0)
	case err != nil:
		usageError(err.Error())
	}
}

// helpHint ends a usage error that the help can explain.
const helpHint = "run scterm -h for the commands and flags"

// parseArgs parses args into cfg through fs, the set newFlagSet made for it.
// It returns flag.ErrHelp when args ask for the help, and a message for the
// user when they hold a mistake.
func parseArgs(fs *flag.FlagSet, cfg *config, args []string) error {
	if len(args) > 0 && args[0] == "help" {
		return flag.ErrHelp
	}
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return err
	case err != nil:
		return fmt.Errorf("%v; %s", err, helpHint)
	}
	if fs.NArg() > 0 {
		return errors.New(leftoverWordsError(fs, args, fs.Args()))
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "peer":
			cfg.usePeer = true
		case "mirror-fps":
			fmt.Fprintln(os.Stderr, "scterm: --mirror-fps does nothing and will be removed")
		}
	})
	return nil
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
	hint := "; " + helpHint
	// A flag written without its dashes, even after an unquoted value with
	// spaces. A single letter is more likely part of a name.
	for _, word := range words {
		if name, _, _ := strings.Cut(word, "="); len(name) > 1 && fs.Lookup(name) != nil {
			suggestion := "--" + word
			if word == "peer" {
				suggestion = "peers, or --peer NAME"
			}
			return fmt.Sprintf("unknown command %q (did you mean %s?)%s", word, suggestion, hint)
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
	if c := closestCommand(w); c != "" {
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

// usageError reports a mistake in how scterm was run and exits with status 1,
// as scrcpy does for a bad option: scterm could not start.
func usageError(msg string) {
	fmt.Fprintf(os.Stderr, "scterm: %s\n", msg)
	os.Exit(1)
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
	// --no-tui means no display at all, and --web and --window are displays.
	if cfg.noTUI && (cfg.web || cfg.window) {
		return errors.New("--web/--window and --no-tui are mutually exclusive")
	}
	if cfg.audioCodec != "" && !slices.Contains(audioCodecs, cfg.audioCodec) {
		return fmt.Errorf("--audio-codec %q is not one of %s", cfg.audioCodec, strings.Join(audioCodecs, ", "))
	}
	if cfg.audioSource != "" && !slices.Contains(audioSources, cfg.audioSource) {
		return fmt.Errorf("--audio-source %q is not one of %s", cfg.audioSource, strings.Join(audioSources, ", "))
	}
	// The server keeps the sound playing on the device only with the playback
	// source (Server.java). As in scrcpy, --audio-dup implies that source when
	// none is named (serverParams), and refuses any other, or no sound at all.
	// A peer device is sent neither the source nor --audio-dup.
	switch {
	case cfg.usePeer:
	case cfg.audioDup && !cfg.audio:
		return errors.New("--audio-dup keeps the sound playing on the device, and --no-audio turns the sound off")
	case cfg.audioDup && cfg.audioSource != "" && cfg.audioSource != "playback":
		return fmt.Errorf("--audio-dup needs --audio-source playback, not %s: only playback capture keeps the sound playing on the device", cfg.audioSource)
	}
	return nil
}
