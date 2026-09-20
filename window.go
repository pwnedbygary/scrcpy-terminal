package main

// --window: pop the web display out into its own top-level window.
//
// The "proprietary magic" here is that we do not ship a toolkit or a Qt/GTK
// webview (which would cost a new dependency and a second, slower video path):
// the display is already a local HTTP+WebSocket endpoint, so the window is a
// browser in app/kiosk mode pointed at it. That gives a standalone window that
// behaves like scrcpy's --window, with hardware video scaling and low-latency
// WebAudio, while the whole video pipeline stays in scterm.
//
// Browser preference order:
//  1. Chromium family (chromium, google-chrome, brave, edge, vivaldi,
//     ungoogled-chromium; or $SCTERM_CHROME / $CHROME_BIN / $CHROMIUM_BIN)
//     -> --app=<url> --window-size=WxH: a standalone, chromeless, correctly
//        sized window. This is the intended experience.
//  2. Firefox -> a temporary profile ("--new-instance --profile"), because
//     Firefox has no way to set a window size from the command line. It opens
//     a normal browser window showing the mirror; $SCTERM_FIREFOX_KIOSK=1
//     switches it to --kiosk for a chromeless, full-screen display.
//
// The initial geometry is derived from the device's rotated display size, so
// the window starts with the phone's aspect ratio (and never larger than it
// needs to be); later viewport changes just rescale the canvas.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// windowGrace is how long a freshly launched browser must survive before its
// exit is read as "the user closed the window" rather than as a failure to
// start. A browser that cannot reach a display dies in about a second.
const windowGrace = 15 * time.Second

// browserCmd is a launch plan for one browser window.
type browserCmd struct {
	name string
	path string
	args []string
	env  []string
	// tempDir, when set, is removed when the window is closed.
	tempDir string
	// profile is a persistent profile directory that is NOT removed on exit.
	profile string
	// firstRun is true when the profile had to be created for this launch.
	firstRun bool
	// note, when set, is a one-line explanation to print before launching
	// (typically: the display had to be recovered from a socket).
	note string
}

// chromiumNames is probed through PATH, in preference order. The Brave
// channel names matter in practice: Brave installs as brave-<channel> (e.g.
// brave-origin-nightly), so a plain "brave" probe misses it entirely.
var chromiumNames = []string{
	"chromium", "chromium-browser", "google-chrome-stable", "google-chrome",
	"brave", "brave-browser",
	"brave-origin", "brave-origin-nightly", "brave-beta", "brave-nightly",
	"microsoft-edge-stable", "microsoft-edge",
	"vivaldi-stable", "vivaldi", "ungoogled-chromium",
}

var chromiumEnvVars = []string{"SCTERM_CHROME", "CHROME_BIN", "CHROMIUM_BIN"}

// chromiumGlobs are fallback locations for browsers that do not put a
// PATH-visible name in front of the real binary. Brave is the motivating case:
// /usr/bin/brave-<channel> may be a wrapper and the actual executable lives at
// /opt/brave.com/<channel>/brave. Chrome and several distro chromium packages
// have the same shape.
var chromiumGlobs = []string{
	"/opt/brave.com/*/brave",
	"/usr/bin/brave*",
	"/opt/google/chrome/chrome",
	"/opt/google/chrome/google-chrome",
	"/usr/lib/chromium/chromium",
	"/usr/lib64/chromium/chromium",
	"/snap/bin/chromium",
}

// findBrowser picks a launcher for the given URL and desired window size.
func findBrowser(url string, w, h int) (browserCmd, error) {
	if p := chromiumPath(); p != "" {
		return chromiumCmd(p, url, w, h), nil
	}
	if p, err := exec.LookPath("firefox"); err == nil {
		return firefoxCmd(p, url, w, h)
	}
	return browserCmd{}, fmt.Errorf("no chromium/chrome or firefox found on PATH")
}

func chromiumPath() string {
	for _, v := range chromiumEnvVars {
		if p := strings.TrimSpace(os.Getenv(v)); p != "" {
			if abs, err := exec.LookPath(p); err == nil {
				return abs
			}
		}
	}
	for _, n := range chromiumNames {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	// Last resort: known install locations (Brave's /opt layout above all).
	// Globs can match directories and .pak files, so filter to runnable files.
	for _, g := range chromiumGlobs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if isRunnableFile(m) {
				return m
			}
		}
	}
	return ""
}

// isRunnableFile reports whether p is a regular (or symlinked) file with an
// execute bit, so browser discovery never returns a directory or an asset.
func isRunnableFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode().Perm()&0o111 != 0
}

// browserEnv returns the environment to launch a browser window with, plus a
// note when no display could be found at all.
//
// It exists because the environment scterm runs in is not necessarily the one a
// GUI application needs. Run from a Zellij pane (which exports no DISPLAY and
// no WAYLAND_DISPLAY, and leaves XDG_SESSION_TYPE=tty behind), Chromium's ozone
// platform picker has nothing to go on: it falls back to X11, finds no X
// server, prints
//
//	ERROR:ui/ozone/platform/x11/ozone_platform_x11.cc:257] Missing X server or $DISPLAY
//	ERROR:ui/aura/env.cc:246] The platform failed to initialize.  Exiting.
//
// and segfaults -- within a second, before a window can exist. Brave's launcher
// script swallows the status (`"$HERE/brave" "$@" || true`), so the only
// symptom was that --window "did not open a window".
//
// The pane is still part of the user's graphical session, so the compositor is
// reachable by socket even when nobody exported the variables: recover the
// display from the sockets, and let an already-complete environment win.
func browserEnv() ([]string, string) {
	env := os.Environ()

	// A Wayland socket we were told about, and that actually exists.
	if wd := os.Getenv("WAYLAND_DISPLAY"); wd != "" {
		if (filepath.IsAbs(wd) && isSocket(wd)) || isSocket(filepath.Join(runtimeDir(), wd)) {
			return env, ""
		}
	}
	// Otherwise prefer Wayland if this user has a compositor at all: it is the
	// session's own path and needs no XWayland in between.
	if sock := findWaylandSocket(); sock != "" {
		env = setEnvKV(env, "WAYLAND_DISPLAY", sock)
		env = setEnvKV(env, "XDG_SESSION_TYPE", "wayland")
		env = setEnvKV(env, "XDG_RUNTIME_DIR", runtimeDir())
		return env, fmt.Sprintf("no DISPLAY/WAYLAND_DISPLAY in this session; using the Wayland compositor %s", sock)
	}
	if d := os.Getenv("DISPLAY"); d != "" {
		if n, ok := x11SocketName(d); ok && isSocket(filepath.Join("/tmp/.X11-unix", "X"+n)) {
			return env, ""
		}
	}
	if d := findX11Display(); d != "" {
		env = setEnvKV(env, "DISPLAY", d)
		env = setEnvKV(env, "XDG_SESSION_TYPE", "x11")
		return env, fmt.Sprintf("no DISPLAY in this session; using the X server %s", d)
	}
	return env, "no Wayland or X socket for this user: no browser window can open here (the web UI still works)"
}

// setEnvKV sets key=value in env, replacing any existing entry: with duplicate
// names in the environment the winner is libc-dependent, so never emit two.
func setEnvKV(env []string, key, val string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// runtimeDir is where the compositor sockets live for this user.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
}

// findWaylandSocket names the user's Wayland compositor socket, if any.
func findWaylandSocket() string {
	matches, _ := filepath.Glob(filepath.Join(runtimeDir(), "wayland-[0-9]*"))
	sort.Strings(matches)
	for _, p := range matches {
		if isSocket(p) { // skips wayland-0.lock
			return filepath.Base(p)
		}
	}
	return ""
}

// x11SocketName turns ":0" or "host:1.0" into the socket suffix ("0", "1").
func x11SocketName(display string) (string, bool) {
	i := strings.LastIndex(display, ":")
	if i < 0 {
		return "", false
	}
	n := display[i+1:]
	if j := strings.IndexByte(n, '.'); j >= 0 {
		n = n[:j]
	}
	if n == "" {
		return "", false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return n, true
}

// x11SocketDir is where X servers advertise their sockets. It is a variable so
// tests can point it at a temp dir instead of the machine's own X server.
var x11SocketDir = "/tmp/.X11-unix"

// findX11Display names an X server (XWayland included) reachable by socket.
func findX11Display() string {
	matches, _ := filepath.Glob(filepath.Join(x11SocketDir, "X*"))
	sort.Strings(matches)
	for _, p := range matches {
		if isSocket(p) {
			return ":" + strings.TrimPrefix(filepath.Base(p), "X")
		}
	}
	return ""
}

func chromiumCmd(path, url string, w, h int) browserCmd {
	// A profile of our own, for the same reason the Firefox path uses one.
	// Sharing the browser's default profile means the launch is handed to an
	// already-running browser of the same family, which ignores --window-size
	// and opens the mirror at its own remembered geometry: the window came up
	// whatever shape it was last left in -- a tall portrait window, say --
	// instead of the size of the output, and the picture was stretched to fill
	// it. A private profile also keeps the mirror out of the real session.
	//
	// It is PERSISTENT rather than a fresh temp dir per run. A brand-new profile
	// has to build its caches from scratch before it can paint, which took the
	// better part of ten seconds of black window on every single launch -- long
	// enough to look like "--window does not work". The same directory on the
	// second run opens in about a second.
	dir, first := chromiumProfileDir()
	if dir == "" {
		// Persistent profile unavailable: a throwaway one still beats sharing
		// the user's, which is the bug this whole block exists to avoid.
		if tmp, err := os.MkdirTemp("", "scterm-chromium-"); err == nil {
			dir, first = tmp, true
		} else {
			fmt.Fprintf(stderrWriter(),
				"scterm: --window: no private browser profile (%v); the window may open at the browser's own size\n", err)
		}
	}
	args := []string{
		"--app=" + url,
		// Distinct WM class so the window is identifiable in a task list.
		"--class=scterm",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate,MediaRouter,OptimizationHints",
		// Audio must start without a click: the device stream is live.
		"--autoplay-policy=no-user-gesture-required",
		// Keep the renderer's frame path tight; we push ~60 JPEGs/s.
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
		"--enable-features=CanvasOopRasterization",
	}
	if dir != "" {
		args = append(args, "--user-data-dir="+dir)
	}
	if w > 0 && h > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", w, h))
	}
	env, note := browserEnv()
	bc := browserCmd{name: "chromium", path: path, args: args, env: env, profile: dir, note: note}
	if first {
		// Only a temp dir is ours to delete; a persistent profile stays.
		if strings.HasPrefix(dir, os.TempDir()) {
			bc.tempDir = dir
		}
		bc.firstRun = true
	}
	return bc
}

// chromiumProfileDir returns a persistent private profile for the window, and
// whether it had to be created (a first run is slow: the browser builds its
// caches before it can show anything).
func chromiumProfileDir() (string, bool) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "scterm", "chromium")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	return dir, true
}

// firefoxCmd uses a dedicated throwaway profile: it must not disturb (or be
// disturbed by) the user's running Firefox, and it needs autoplay allowed so
// the device audio starts without a click.
func firefoxCmd(path, url string, w, h int) (browserCmd, error) {
	dir, err := os.MkdirTemp("", "scterm-fx-")
	if err != nil {
		return browserCmd{}, err
	}
	prefs := `// scterm --window profile: throwaway, autoplay allowed, no distraction.
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.startup.page", 0);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("toolkit.telemetry.enabled", false);
user_pref("media.autoplay.default", 0);
user_pref("media.autoplay.blocking_policy", 0);
user_pref("media.autoplay.allow-muted", true);
user_pref("browser.tabs.warnOnClose", false);
user_pref("browser.warnOnQuit", false);
user_pref("browser.sessionstore.resume_from_crash", false);
user_pref("dom.disable_beforeunload", true);
user_pref("full-screen-api.warning.timeout", 0);
user_pref("browser.fullscreen.autohide", true);
`
	if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(prefs), 0o600); err != nil {
		os.RemoveAll(dir)
		return browserCmd{}, err
	}
	// Firefox cannot be told a window size from the command line (its
	// --window-size only applies to --screenshot). The page reports its own
	// viewport, so the mirror fits whatever window it gets; window-size is
	// only used to position a first-run window via the saved profile state
	// below when kiosk is off.
	args := []string{
		"--profile", dir,
		"--new-instance",
	}
	if os.Getenv("SCTERM_FIREFOX_KIOSK") != "" {
		args = append(args, "--kiosk")
	} else {
		args = append(args, "--new-window")
	}
	args = append(args, url)
	_ = w
	_ = h
	env, note := browserEnv()
	env = setEnvKV(env, "MOZ_CRASHREPORTER_DISABLE", "1")
	return browserCmd{name: "firefox", path: path, args: args, env: env, tempDir: dir, note: note}, nil
}

// openWindow launches the browser window and returns immediately: the process
// outlives nothing important (the server is the parent), and window mode ends
// when the last client disconnects.
func (s *webServer) openWindow(url string) error {
	w, h := s.windowGeometry()
	bc, err := findBrowser(url, w, h)
	if err != nil {
		return err
	}
	if bc.note != "" {
		fmt.Fprintf(stderrWriter(), "scterm: window: %s\n", bc.note)
	}

	// Keep the browser's own output: it is the only explanation available when
	// a browser refuses to start. It goes to a file rather than through a Go
	// pipe (a pipe would make Wait block until every inherited copy of the
	// write end is gone) and to a file rather than to the terminal, because a
	// working browser is chatty -- Vulkan, dbus and fontconfig warnings.
	logPath, logFile, logErr := openWindowLog()
	if logErr != nil {
		logPath = ""
	}

	cmd := exec.Command(bc.path, bc.args...)
	cmd.Env = bc.env
	// Detach from the terminal's process group so a Ctrl-C in the shell does
	// not kill the window before scterm can shut down cleanly; the group is
	// also what lets shutdown take the whole browser down at once -- launcher
	// script, browser and renderers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		if bc.tempDir != "" {
			os.RemoveAll(bc.tempDir)
		}
		return err
	}
	if logFile != nil {
		logFile.Close() // the browser holds its own descriptor now
	}
	s.setWindow(cmd, logPath)
	if bc.name == "firefox" {
		if os.Getenv("SCTERM_FIREFOX_KIOSK") != "" {
			fmt.Fprintf(stderrWriter(), "scterm: window: firefox kiosk (%s)\n", bc.path)
		} else {
			fmt.Fprintf(stderrWriter(), "scterm: window: firefox (%s); install chromium for a chromeless, device-sized window\n", bc.path)
		}
	} else {
		fmt.Fprintf(stderrWriter(), "scterm: window: %s (%dx%d)\n", bc.path, w, h)
		if bc.firstRun {
			// Say it out loud: on a first run the browser has to build a private
			// profile before it can paint, and a window that stays black for ten
			// seconds reads as "the window never opened".
			fmt.Fprintf(stderrWriter(),
				"scterm: window: first run of the private profile (%s) - it can take a few seconds to appear; later runs are fast\n",
				bc.profile)
		}
	}
	go s.watchWindow(bc, cmd, logPath, time.Now())
	return nil
}

// watchWindow waits for the browser and explains a launch that never worked.
//
// Before this existed the launch was fire-and-forget with the browser's stdio
// on /dev/null: a browser that died in its first second (no display, a broken
// GPU stack, a profile lock held by another instance) left a cheerful "window:
// /usr/bin/brave-origin-nightly (1280x720)" on the terminal, no window, and
// nothing anywhere saying why.
func (s *webServer) watchWindow(bc browserCmd, cmd *exec.Cmd, logPath string, start time.Time) {
	err := cmd.Wait()
	s.clearWindow(cmd)
	if bc.tempDir != "" {
		os.RemoveAll(bc.tempDir)
	}
	report, keepLog := windowExitReport(bc, time.Since(start), err, windowLogText(logPath), s.hadClient.Load(), s.url)
	if report == nil {
		removeFile(logPath) // a closed window has nothing to explain
		return
	}
	for _, line := range report {
		fmt.Fprintln(stderrWriter(), line)
	}
	if keepLog && logPath != "" {
		fmt.Fprintf(stderrWriter(), "scterm: window: browser output: %s\n", logPath)
	} else {
		removeFile(logPath)
	}
}

// windowExitReport describes a browser window that has exited, returning nil
// when there is nothing to say, and whether the captured browser log is worth
// keeping for the user.
//
// Nothing to say covers the two healthy endings: a window that lived long
// enough to have shown the mirror, and one that demonstrably did (a viewer
// connected at some point) and was then closed by the user.
func windowExitReport(bc browserCmd, elapsed time.Duration, exitErr error, log string, hadClient bool, url string) (report []string, keepLog bool) {
	if elapsed >= windowGrace || hadClient {
		return nil, false
	}
	// A Chromium handed a command line for a profile that is already open does
	// not start a browser: it forwards the URL to the running instance, says
	// so, and exits 0 in about 60ms. No new window is the expected outcome.
	if strings.Contains(log, "existing browser session") {
		if pid := profileLockPid(bc.profile); pid > 0 {
			return []string{fmt.Sprintf(
				"scterm: window: this private profile is held by a running window (pid %d); the mirror was handed over to it instead of opening a new window", pid)}, false
		}
		return []string{"scterm: window: a browser is already running on this private profile; the mirror was handed over to it instead of opening a new window"}, false
	}
	report = append(report, fmt.Sprintf(
		"scterm: window: the browser exited after %.1fs without displaying anything (%s)",
		elapsed.Seconds(), exitStatusString(exitErr)))
	for _, line := range windowLogProblems(log) {
		report = append(report, "scterm: window:   "+line)
	}
	if url != "" {
		report = append(report, "scterm: the mirror is still served at "+url)
	}
	return report, true
}

func exitStatusString(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// openWindowLog creates the file the browser's output is captured to.
func openWindowLog() (string, *os.File, error) {
	f, err := os.CreateTemp("", "scterm-browser-*.log")
	if err != nil {
		return "", nil, err
	}
	return f.Name(), f, nil
}

func removeFile(p string) {
	if p != "" {
		os.Remove(p)
	}
}

// profileLockPid returns the pid holding a Chromium profile's singleton lock,
// or 0 when the lock is absent or its owner is gone. The lock is a symlink to
// "<hostname>-<pid>".
func profileLockPid(profile string) int {
	if profile == "" {
		return 0
	}
	target, err := os.Readlink(filepath.Join(profile, "SingletonLock"))
	if err != nil {
		return 0
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return 0
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return 0
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return 0 // stale: the owner is gone, and Chromium takes the lock over
	}
	return pid
}

// windowLogText reads the captured browser output, keeping the tail when a
// long-running browser made it big.
func windowLogText(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	const maxRead = 64 << 10
	if len(data) > maxRead {
		data = data[len(data)-maxRead:]
	}
	return string(data)
}

// windowLogProblems picks the lines of a browser log that explain a failure:
// the error-ish lines when there are any, otherwise the last few lines.
func windowLogProblems(log string) []string {
	if log == "" {
		return nil
	}
	var problems, last []string
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if len(line) > 240 {
			line = line[:240] + "..."
		}
		if len(last) == 6 {
			last = last[1:]
		}
		last = append(last, line)
		low := strings.ToLower(line)
		if strings.Contains(low, "error") || strings.Contains(low, "fatal") ||
			strings.Contains(low, "missing") || strings.Contains(low, "segmentation") ||
			strings.Contains(low, "cannot load") || strings.Contains(low, "failed to initialize") {
			if len(problems) == 6 {
				problems = problems[1:]
			}
			problems = append(problems, line)
		}
	}
	if len(problems) > 0 {
		return problems
	}
	return last
}

// setWindow remembers the browser we launched so shutdown can close it.
func (s *webServer) setWindow(cmd *exec.Cmd, logPath string) {
	s.winMu.Lock()
	s.win, s.winLog = cmd, logPath
	s.winMu.Unlock()
}

func (s *webServer) clearWindow(cmd *exec.Cmd) {
	s.winMu.Lock()
	if s.win == cmd {
		s.win, s.winLog = nil, ""
	}
	s.winMu.Unlock()
}

// killWindow closes the window we opened. The whole process group goes down
// together (launcher script, browser, renderers); the browser gets a moment to
// exit on SIGTERM first, because a half-dead browser would keep the private
// profile locked.
//
// The wait is bounded and synchronous on purpose. This runs on the way out of
// the process, so a SIGKILL left to a timer goroutine would never fire -- the
// process is gone by then, and the orphaned browser would hold the profile
// against the next --window run. A browser that is still responsive costs one
// poll interval (~50ms); only one that ignores SIGTERM pays the full wait.
func (s *webServer) killWindow() {
	s.winMu.Lock()
	cmd, logPath := s.win, s.winLog
	s.win, s.winLog = nil, ""
	s.winMu.Unlock()
	if s.hadClient.Load() {
		// The window worked; its log is browser chatter, and this process is on
		// its way out, so the watcher goroutine may never get to remove it.
		removeFile(logPath)
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return // no such group: it is already gone
	}
	for i := 0; i < windowKillGracePolls; i++ {
		if syscall.Kill(-pgid, 0) != nil {
			return // every process in the group has exited
		}
		time.Sleep(windowKillPoll)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

const (
	windowKillPoll = 50 * time.Millisecond
	// windowKillGracePolls is how many poll intervals the browser gets to die
	// on SIGTERM before the group is killed outright (1.5s).
	windowKillGracePolls = 30
)

// windowGeometry returns the initial window size in CSS pixels. It matches the
// video the host actually sends -- the device display scaled so its long side is
// at most --max-size, exactly the rule the scrcpy server applies -- so the
// window opens at the stream's own resolution and the mirror is 1:1. Sizing it
// from the raw device display instead (1920x1080 against a 1280x720 capture)
// made the window bigger than the picture, which is what framed it in black.
func (s *webServer) windowGeometry() (int, int) {
	if sz := strings.TrimSpace(s.cfg.windowSize); sz != "" {
		if w, h, ok := parseWindowSize(sz); ok {
			return w, h
		}
		fmt.Fprintf(stderrWriter(), "scterm: --window-size %q is not WxH; using the device size\n", sz)
	}
	w, h := 0, 0
	if s.sess != nil && s.sess.adb != nil {
		if dw, dh := s.sess.adb.deviceDisplaySize(); dw > 0 && dh > 0 {
			w, h = dw, dh
		}
	}
	if w <= 0 || h <= 0 {
		w, h = 480, 1080
	}
	// --max-size caps the capture, so it caps the canvas the browser draws.
	// Mirroring it here is what makes the window match the picture.
	if m := s.cfg.maxSize; m > 0 && max(w, h) > m {
		scale := float64(m) / float64(max(w, h))
		w, h = int(float64(w)*scale), int(float64(h)*scale)
	}
	w, h = w&^1, h&^1 // even, like the capture: JPEG 4:2:0 needs it
	const maxH = 1000 // keep it inside a 1080p desktop with room for a title bar
	if h > maxH {
		scale := float64(maxH) / float64(h)
		w = int(float64(w) * scale)
		h = maxH
	}
	if w < 200 {
		w = 200
	}
	return w, h
}

func parseWindowSize(s string) (int, int, bool) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' || r == ',' })
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w < 100 || h < 100 || w > 8192 || h > 8192 {
		return 0, 0, false
	}
	return w, h, true
}
