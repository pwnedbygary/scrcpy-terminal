package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"scterm/peer"
)

// doctorLine is one line of scterm doctor: how a check went (ok, warn or
// FAIL), what it checked, what it found, and what to do, when there is
// something to do.
type doctorLine struct {
	status, check, detail, todo string
}

const (
	doctorOK   = "ok"
	doctorWarn = "warn"
	doctorFail = "FAIL"
)

// runDoctorCommand runs `scterm doctor`, and reports whether args named it.
// A failed check exits 1, once every line is printed.
func runDoctorCommand(args []string) bool {
	if len(args) == 0 || args[0] != "doctor" {
		return false
	}
	if len(args) > 1 {
		if isHelpArg(args[1]) {
			fmt.Print(doctorHelp)
			return true
		}
		usageError("usage: scterm doctor (it takes no arguments)")
	}
	if runDoctor(os.Stdout, defaultConfig()) {
		os.Exit(1)
	}
	return true
}

const doctorHelp = `usage: scterm doctor

Checks what scterm needs on this computer, and prints one line for each
check: ok, warn or FAIL, what it checked and what it found, and under it
what to do, when there is something to do. It checks this build (on x86-64,
that the CPU has AVX2), adb and each device it lists, with the Android
version of each ready one, the sound of the terminal display, the browser
and the display --window would use, whether --web can listen on its default
address, 127.0.0.1:6969, the names in SCTERM_WEB_HOSTS, and the identity and
pairings of peer mode. It connects to no paired device: scterm peers --check
does. It exits 1 when a check fails, and 0 when there are only warnings.
`

// runDoctor runs scterm doctor's checks, in the order it prints them, and
// prints the lines of each to w as soon as it has run. It reports whether a
// check failed. cfg is the configuration scterm starts from, whose web address
// and port it checks.
func runDoctor(w io.Writer, cfg config) (failed bool) {
	show := func(lines ...doctorLine) { failed = printDoctor(w, lines) || failed }
	// How much a missing adb matters depends on the pairings, so they are
	// read first, and shown last.
	peers, paired := doctorPeers()
	show(doctorBuild(runtime.GOARCH))
	adb := doctorADB(paired > 0)
	show(adb)
	if adb.status == doctorOK {
		show(doctorDevices(paired > 0)...)
	}
	show(doctorSound()...)
	show(doctorWindow()...)
	show(doctorWebPort(cfg.webAddr, cfg.webPort))
	show(doctorWebHosts()...)
	show(peers...)
	return failed
}

// printDoctor writes lines to w, each with what to do under it, and reports
// whether a check failed. What programs and devices said reaches the
// terminal without control characters.
func printDoctor(w io.Writer, lines []doctorLine) (failed bool) {
	for _, l := range lines {
		fmt.Fprintf(w, "%-4s  %-6s  %s\n", l.status, l.check, cleanPeerText(l.detail))
		if l.todo != "" {
			fmt.Fprintf(w, "%14s%s\n", "", cleanPeerText(l.todo))
		}
		failed = failed || l.status == doctorFail
	}
	return failed
}

// cpuinfoPath is where scterm doctor reads the CPU's flags; tests point it at
// their own.
var cpuinfoPath = "/proc/cpuinfo"

// doctorBuild names this build, and on amd64 checks that the CPU has AVX2,
// which the C code of every amd64 build is compiled to use (-mavx2).
func doctorBuild(arch string) doctorLine {
	l := doctorLine{doctorOK, "build", fmt.Sprintf("scterm %s for %s/%s", version, runtime.GOOS, arch), ""}
	if arch != "amd64" {
		return l
	}
	flags, err := cpuFlags(cpuinfoPath)
	switch {
	case err != nil:
		l.status = doctorWarn
		l.detail += fmt.Sprintf("; whether the CPU has AVX2 is unknown (%v)", err)
		l.todo = "scterm needs AVX2 on x86-64: without it, it stops with SIGILL: illegal instruction"
	case !slices.Contains(flags, "avx2"):
		l.status = doctorFail
		l.detail += "; the CPU has no AVX2, which every x86-64 build of scterm uses"
		l.todo = "scterm cannot run on this CPU, built from source or not: it stops with SIGILL: illegal instruction"
	default:
		l.detail += "; the CPU has AVX2"
	}
	return l
}

// cpuFlags reads the flags a /proc/cpuinfo lists for its first CPU.
func cpuFlags(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "flags" {
			return strings.Fields(value), nil
		}
	}
	return nil, fmt.Errorf("%s lists no CPU flags", path)
}

// doctorTimeout bounds each program scterm doctor runs, so that a stuck adb
// or sound server cannot hold it up.
var doctorTimeout = 10 * time.Second

// doctorOutput runs a program in the C locale, so that its output reads the
// same everywhere, with env added to its environment, and returns what it
// printed. Its error is the cause the program wrote to stderr, when it wrote
// one.
func doctorOutput(env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Whatever the program leaves running must not keep Wait waiting.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // it exited 0; what it left running held its output open
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return "", fmt.Errorf("no answer within %v", doctorTimeout)
	case err != nil:
		if cause := stderrCause(stderr.String()); cause != "" {
			return string(out), errors.New(cause)
		}
		return string(out), err
	}
	return string(out), nil
}

// stderrCause is the cause a program gave on stderr: the first line that is
// not blank after the last of the progress lines adb writes, which start with
// "* " ("* daemon not running; starting now at tcp:5037", then "* failed to
// start daemon": client/adb_client.cpp, lines 262-281 at 1cf2f017). After
// those adb writes the error it stops with.
func stderrCause(s string) string {
	cause := ""
	for _, l := range strings.Split(s, "\n") {
		switch l = strings.TrimSpace(l); {
		case strings.HasPrefix(l, "* "):
			cause = ""
		case cause == "":
			cause = l
		}
	}
	return cause
}

// headLines is the first n lines of s that are not blank, trimmed.
func headLines(s string, n int) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && len(lines) < n {
			lines = append(lines, l)
		}
	}
	return lines
}

// withoutADB is how much a problem matters that --peer avoids: a paired
// device can be reached without adb, so with one it is a warning.
func withoutADB(paired bool) string {
	if paired {
		return doctorWarn
	}
	return doctorFail
}

// orPeer is todo, and with a paired device, --peer as well.
func orPeer(todo string, paired bool) string {
	if paired {
		return todo + ", or use --peer to reach the scterm Android app without it"
	}
	return todo
}

// doctorADB checks that adb is on PATH and runs. Without it, the advice
// offers --peer; with a paired device, so does the advice for an adb that does
// not run.
func doctorADB(paired bool) doctorLine {
	path, err := exec.LookPath("adb")
	if err != nil {
		return doctorLine{withoutADB(paired), "adb", "not found on PATH", adbMissingHint}
	}
	out, err := doctorOutput(nil, "adb", "version")
	if err != nil {
		return doctorLine{withoutADB(paired), "adb", fmt.Sprintf("%s does not run (%v)", path, err),
			orPeer("install Android platform-tools again", paired)}
	}
	detail := path
	if v := headLines(out, 2); len(v) > 0 {
		detail += ": " + strings.Join(v, ", ")
	}
	return doctorLine{doctorOK, "adb", detail, ""}
}

// serialForms says which forms adb takes for -s and ANDROID_SERIAL
// (adbDevice.matchesTarget).
const serialForms = "connect that device, or unset ANDROID_SERIAL, or set it as adb takes it: a serial, a network device's host " +
	"without its port (tcp: or udp: may come first), a USB path such as usb:1-1, or product:, model: or device: and a name, as adb devices -l shows them"

// doctorDevices checks each device adb lists, and the Android version of each
// ready one. A device's problem is a failure when it is the device scterm
// would use (the one ANDROID_SERIAL names, or the only ready one), or when no
// device is chosen and scterm has none it can mirror; otherwise it is a
// warning. No device at all offers --peer. With a paired device, no device
// and a listing that fails are warnings, and the advice for the listing
// offers --peer too.
func doctorDevices(paired bool) []doctorLine {
	out, err := doctorOutput(nil, "adb", "devices", "-l")
	if err != nil {
		return []doctorLine{{withoutADB(paired), "device", fmt.Sprintf("adb devices -l failed (%v)", err),
			orPeer("restart adb with 'adb kill-server', then try again", paired)}}
	}
	devices := listedDevices(out)
	lines := make([]doctorLine, len(devices))
	unusable := make([]bool, len(devices)) // not ready, or too old to mirror
	var ready []string
	mirrorable := false // whether a ready device may be one scterm can mirror
	for i, d := range devices {
		if d.state == "device" {
			ready = append(ready, d.serial)
			lines[i], unusable[i] = doctorDevice(d)
			mirrorable = mirrorable || !unusable[i]
			continue
		}
		why, todo := notReadyAdvice(d.serial, d.state)
		lines[i], unusable[i] = doctorLine{doctorWarn, "device", why, todo}, true
	}
	// The device scterm would use, as findDevice picks it: the one adb takes
	// for ANDROID_SERIAL, or else the only ready one.
	chosen := envOr("ANDROID_SERIAL", "")
	picked, ambiguous := pickTarget(devices, chosen)
	for i, d := range devices {
		named := !ambiguous && slices.Contains(picked, i)
		use := named || chosen == "" && len(ready) == 1 && d.state == "device"
		if unusable[i] && (use || chosen == "" && !mirrorable) {
			lines[i].status = doctorFail
		}
		switch {
		case named && chosen == d.serial:
			lines[i].detail += "; ANDROID_SERIAL names it"
		case named:
			lines[i].detail += "; ANDROID_SERIAL=" + chosen + " names it"
		}
	}
	switch {
	case chosen != "" && len(picked) == 0:
		lines = append(lines, doctorLine{doctorFail, "device", "ANDROID_SERIAL is " + chosen + ", which names no device adb lists", serialForms})
	case ambiguous:
		var serials []string
		for _, i := range picked {
			serials = append(serials, devices[i].serial)
		}
		lines = append(lines, doctorLine{doctorFail, "device",
			fmt.Sprintf("ANDROID_SERIAL is %s, which names %d devices (%s), so adb takes none of them", chosen, len(picked), strings.Join(serials, ", ")),
			"set it to the serial of the one to use"})
	case len(devices) == 0:
		lines = append(lines, doctorLine{withoutADB(paired), "device", "no device found", noDeviceHint})
	case chosen == "" && len(ready) > 1:
		lines = append(lines, doctorLine{doctorWarn, "device", severalDevices(ready), chooseDeviceHint + ", or set ANDROID_SERIAL"})
	}
	return lines
}

// doctorDevice reads a ready device's Android version, read-only, and compares
// it with what the README gives: Android 5.0 (SDK 21) for the bundled server,
// 11 (SDK 30) for sound and 13 (SDK 33) for playback capture. It reports
// whether the device is too old for the bundled server; doctorDevices decides
// whether that fails.
func doctorDevice(d adbDevice) (l doctorLine, tooOld bool) {
	name := d.serial
	if d.model != "" {
		name += " (" + d.model + ")"
	}
	release, err := doctorOutput(nil, "adb", "-s", d.serial, "shell", "getprop", "ro.build.version.release")
	sdk := 0
	if err == nil {
		var out string
		if out, err = doctorOutput(nil, "adb", "-s", d.serial, "shell", "getprop", "ro.build.version.sdk"); err == nil {
			if sdk, err = strconv.Atoi(strings.TrimSpace(out)); err != nil {
				err = fmt.Errorf("its SDK reads %q", strings.TrimSpace(out))
			}
		}
	}
	if err != nil {
		return doctorLine{doctorWarn, "device", fmt.Sprintf("%s is ready, but its Android version could not be read (%v)", name, err),
			"see what adb says: adb -s " + d.serial + " shell getprop ro.build.version.sdk"}, false
	}
	android := fmt.Sprintf("SDK %d", sdk)
	if r := strings.TrimSpace(release); r != "" {
		android = fmt.Sprintf("Android %s (SDK %d)", r, sdk)
	}
	l = doctorLine{doctorOK, "device", name + " is ready: " + android, ""}
	switch {
	case sdk < 21:
		l.status = doctorWarn
		l.detail += "; the bundled server needs Android 5.0 or later"
		l.todo = "scterm cannot mirror it: use a device with Android 5.0 or later"
		tooOld = true
	case sdk < 30:
		l.status = doctorWarn
		l.detail += "; it sends no sound, which needs Android 11 or later"
		l.todo = "scterm shows and controls it without sound"
	case sdk < 33:
		l.detail += "; sound works, but playback capture (--audio-source playback, --audio-dup) needs Android 13 or later"
		if sdk == 30 {
			l.todo = "unlock it before you start scterm, for sound"
		}
	default:
		l.detail += "; sound and playback capture work"
	}
	return l, tooOld
}

// noServerTodo is what to do when pactl reaches no sound server.
const noServerTodo = "start PulseAudio, or PipeWire with its PulseAudio service (pipewire-pulse); the browser display (--web, --window) plays the sound without it"

// doctorSound checks what the terminal display's sound needs on this
// computer: pactl, a sound server that pactl reaches, XDG_RUNTIME_DIR, and the
// sink SCT_AUDIO_DEVICE names, if any. Each problem is a warning: scterm plays
// on the server's default output without pactl, and the browser display plays
// the sound without the rest.
func doctorSound() []doctorLine {
	var lines []doctorLine
	var env []string // what pactl reached the sound server with
	reached := false
	if _, err := exec.LookPath("pactl"); err != nil {
		lines = append(lines, doctorLine{doctorWarn, "sound",
			"pactl not found on PATH; without it, scterm plays on the sound server's default output, and the sound moves to another output only when the server moves it",
			"install pactl (pulseaudio-utils on Debian and Ubuntu), so that the sound follows the output you pick"})
	} else {
		var l doctorLine
		l, env, reached = doctorPulse()
		lines = append(lines, l)
	}
	if !reached && os.Getenv("XDG_RUNTIME_DIR") == "" {
		lines = append(lines, doctorLine{doctorWarn, "sound", "XDG_RUNTIME_DIR is not set, and the sound server's socket is normally in it",
			"run scterm from a terminal of your desktop session, where it is set"})
	}
	if sink := os.Getenv("SCT_AUDIO_DEVICE"); sink != "" {
		lines = append(lines, doctorSink(sink, env, reached))
	}
	return lines
}

// doctorPulse asks pactl for the sound server where scterm's stream looks for
// one (audioServers): libpulse's default, which PULSE_SERVER sets, and when
// PULSE_SERVER reaches none, the user's socket by name. It returns the line,
// what pactl reached the server with, and whether it reached one.
func doctorPulse() (doctorLine, []string, bool) {
	info, err := doctorOutput(nil, "pactl", "info")
	if err == nil {
		return doctorLine{doctorOK, "sound", pactlReaches(info), ""}, nil, true
	}
	set := os.Getenv("PULSE_SERVER")
	if set == "" {
		return doctorLine{doctorWarn, "sound", fmt.Sprintf("pactl reaches no sound server (%v)", err), noServerTodo}, nil, false
	}
	detail := fmt.Sprintf("pactl reaches no sound server at PULSE_SERVER=%s (%v)", set, err)
	socket := pulseSocketPath()
	if socket == "" || socket == set {
		return doctorLine{doctorWarn, "sound", detail, noServerTodo}, nil, false
	}
	env := []string{"PULSE_SERVER=" + socket}
	info, err = doctorOutput(env, "pactl", "info")
	if err != nil {
		return doctorLine{doctorWarn, "sound", fmt.Sprintf("%s, nor at %s (%v)", detail, socket, err), noServerTodo}, nil, false
	}
	return doctorLine{doctorWarn, "sound", detail + "; scterm falls back to " + socket + ", where " + pactlReaches(info),
		"unset PULSE_SERVER, or set it to a sound server that runs"}, env, true
}

// pactlReaches says which sound server pactl info reached, and its default
// output.
func pactlReaches(info string) string {
	server := pactlField(info, "Server Name")
	if server == "" {
		server = "a sound server"
	}
	s := "pactl reaches " + server
	if sink := pactlField(info, "Default Sink"); sink != "" {
		s += "; the default output is " + sink
	}
	return s
}

// pactlField is the value of a "Name: value" line of pactl info.
func pactlField(info, name string) string {
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// doctorSink checks that the sound server has the sink SCT_AUDIO_DEVICE names,
// when pactl reached one to ask, with env.
func doctorSink(sink string, env []string, reached bool) doctorLine {
	if !reached {
		return doctorLine{doctorOK, "sound", "SCT_AUDIO_DEVICE names the sink " + sink + " (not checked)", ""}
	}
	out, err := doctorOutput(env, "pactl", "list", "sinks", "short")
	if err != nil {
		return doctorLine{doctorWarn, "sound", fmt.Sprintf("SCT_AUDIO_DEVICE names the sink %s, which could not be checked (%v)", sink, err),
			"pactl list sinks short lists the sinks"}
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == sink {
			return doctorLine{doctorOK, "sound", "SCT_AUDIO_DEVICE names the sink " + sink + ", which the sound server has", ""}
		}
	}
	return doctorLine{doctorWarn, "sound", "SCT_AUDIO_DEVICE names " + sink + ", which is not one of the sound server's sinks",
		"set it to a name that pactl list sinks short shows, or unset it to follow the desktop's output"}
}

// doctorWindow checks what --window needs: the browser it would start, found
// as it finds it, and a display to open it on.
func doctorWindow() []doctorLine {
	var lines []doctorLine
	from := "" // the variable that names the browser, if one does
	for _, v := range chromiumEnvVars {
		p := strings.TrimSpace(os.Getenv(v))
		if p == "" {
			continue
		}
		if _, err := exec.LookPath(p); err == nil {
			from = v
			break
		}
		lines = append(lines, doctorLine{doctorWarn, "window", fmt.Sprintf("%s is %q, which is not a program scterm can start", v, p),
			"set it to a Chromium-family browser, by name or path, or unset it"})
	}
	switch name, path := windowBrowser(); {
	case name == "chromium" && from != "":
		lines = append(lines, doctorLine{doctorOK, "window", fmt.Sprintf("browser %s (%s)", path, from), ""})
	case name == "chromium":
		lines = append(lines, doctorLine{doctorOK, "window", "browser " + path, ""})
	case name == "firefox":
		how := "in a normal window"
		if os.Getenv("SCTERM_FIREFOX_KIOSK") != "" {
			how = "full screen (SCTERM_FIREFOX_KIOSK)"
		}
		lines = append(lines, doctorLine{doctorOK, "window", "browser " + path + ", " + how,
			"install a Chromium-family browser for a window without toolbars, sized to the device"})
	default:
		lines = append(lines, doctorLine{doctorWarn, "window", "no Chromium-family browser or Firefox found",
			"install one, or set SCTERM_CHROME; --web works with any browser"})
	}
	return append(lines, doctorDisplay())
}

// doctorDisplay checks for a display that a browser window can open on, as
// --window finds one: by WAYLAND_DISPLAY or DISPLAY, or else by the sockets of
// the user's session.
func doctorDisplay() doctorLine {
	_, note := browserEnv()
	switch {
	case note == "":
		var set []string
		for _, v := range []string{"WAYLAND_DISPLAY", "DISPLAY"} {
			if s := os.Getenv(v); s != "" {
				set = append(set, v+"="+s)
			}
		}
		return doctorLine{doctorOK, "window", "display " + strings.Join(set, ", "), ""}
	case findWaylandSocket() != "" || findX11Display() != "":
		return doctorLine{doctorOK, "window", note, ""}
	}
	return doctorLine{doctorWarn, "window", note, "run scterm in your desktop session, or use --web and open its address in a browser"}
}

// doctorWebPort checks that the browser display can listen on addr and port,
// by listening there and closing at once.
func doctorWebPort(addr string, port int) doctorLine {
	ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err == nil {
		ln.Close()
		return doctorLine{doctorOK, "web", fmt.Sprintf("port %d on %s is free", port, addr), ""}
	}
	const another = "choose another port with --web-port PORT (0 picks a free one)"
	if errors.Is(err, syscall.EADDRINUSE) {
		return doctorLine{doctorWarn, "web", fmt.Sprintf("port %d on %s is in use", port, addr), "stop the program that uses it, or " + another}
	}
	return doctorLine{doctorWarn, "web", fmt.Sprintf("cannot listen on port %d on %s (%v)", port, addr, err), another}
}

// doctorWebHosts reads SCTERM_WEB_HOSTS as the browser display does
// (webHostNames), and says which names it adds, each once, in the order given,
// and each entry the display ignores, with the advice the display prints.
func doctorWebHosts() []doctorLine {
	hosts := os.Getenv(webHostsEnv)
	served, ignored := webHostNames(nil, "", "", hosts)
	var names []string
	for _, s := range strings.Split(hosts, ",") {
		if name, ok := webHostName(strings.TrimSpace(s)); ok && served[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	var lines []doctorLine
	if len(names) > 0 {
		lines = append(lines, doctorLine{doctorOK, "web", webHostsEnv + " adds " + strings.Join(names, ", "), ""})
	}
	for _, e := range ignored {
		lines = append(lines, doctorLine{doctorWarn, "web", fmt.Sprintf("%s has %+q, which scterm ignores", webHostsEnv, e),
			webHostsAdvice(e)})
	}
	return lines
}

// unreadableTodo is what to do about a file of peer mode's that cannot be
// read: when permissions are why, giving the file and its directory back the
// modes scterm makes them with keeps the pairings; otherwise, aside says what
// to do.
func unreadableTodo(path string, err error, aside string) string {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Sprintf("let your user read it again (as their owner: chmod 700 %s; chmod 600 %s); that keeps the pairings", filepath.Dir(path), path)
	}
	return aside
}

// doctorPeers checks peer mode's identity and pairings where scterm keeps them,
// without creating either or connecting to any device, and returns how many
// devices are paired.
func doctorPeers() ([]doctorLine, int) {
	home, err := peerHome()
	if err != nil {
		return []doctorLine{{doctorWarn, "peers", fmt.Sprintf("no place to keep peer mode's identity and pairings (%v)", err),
			"set HOME, or set SCTERM_HOME to a directory for them"}}, 0
	}
	where := home
	if os.Getenv("SCTERM_HOME") != "" {
		where += " (SCTERM_HOME)"
	}
	const pairHint = "scterm pair pairs with a device that runs the scterm app, to reach it without adb"
	recs, storeErr := peer.OpenStore(home).All()
	path := filepath.Join(home, "identity.pem")
	info, statErr := os.Stat(path)
	noIdentity := errors.Is(statErr, fs.ErrNotExist)
	if noIdentity && storeErr == nil && len(recs) == 0 {
		return []doctorLine{{doctorOK, "peers", "nothing paired yet; pairings are kept in " + where, pairHint}}, 0
	}
	var lines []doctorLine
	idTodo := "move " + path + " aside, then pair each device again (scterm pair makes a new identity)"
	switch {
	case noIdentity && len(recs) > 0:
		lines = append(lines, doctorLine{doctorFail, "peers", fmt.Sprintf("no identity in %s, so the paired devices no longer know this computer", where),
			"pair each device again with scterm pair"})
	case noIdentity:
		lines = append(lines, doctorLine{doctorOK, "peers", "no identity in " + where + " yet; scterm pair makes one", ""})
	case statErr != nil:
		lines = append(lines, doctorLine{doctorFail, "peers", statErr.Error(), unreadableTodo(path, statErr, idTodo)})
	default:
		// The file is there, so this only loads it.
		id, err := peer.LoadOrCreateIdentity(home)
		if err != nil {
			lines = append(lines, doctorLine{doctorFail, "peers", err.Error(), unreadableTodo(path, err, idTodo)})
			break
		}
		lines = append(lines, doctorLine{doctorOK, "peers", fmt.Sprintf("this computer is %s (identity in %s)", id.Fingerprint.Short(), where), ""})
		// scterm writes it with mode 0600: it holds this computer's private key.
		if perm := info.Mode().Perm(); perm&0o044 != 0 {
			lines = append(lines, doctorLine{doctorWarn, "peers", fmt.Sprintf("other users can read %s (mode %04o), which holds this computer's private key", path, perm),
				"chmod 600 " + path})
		}
	}
	switch n := len(recs); {
	case storeErr != nil:
		pj := filepath.Join(home, "peers.json")
		lines = append(lines, doctorLine{doctorFail, "peers", storeErr.Error(), unreadableTodo(pj, storeErr, "move peers.json aside, then pair each device again")})
	case n == 0:
		lines = append(lines, doctorLine{doctorOK, "peers", "no paired devices", pairHint})
	case n == 1:
		lines = append(lines, doctorLine{doctorOK, "peers", "1 paired device: " + peer.Names(recs), "scterm peers --check tests whether it is reachable"})
	default:
		lines = append(lines, doctorLine{doctorOK, "peers", fmt.Sprintf("%d paired devices: %s", n, peer.Names(recs)), "scterm peers --check tests whether each one is reachable"})
	}
	return lines, len(recs)
}
