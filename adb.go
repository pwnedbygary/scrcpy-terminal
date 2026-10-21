package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

func lookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

func osMkdirTemp(pattern, name string) (string, error) { return os.MkdirTemp(pattern, name) }

func osRemoveAll(path string) error { return os.RemoveAll(path) }

func osWriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

// adbRunner wraps exec of the adb binary.
type adbRunner struct {
	serial string
}

func newADB(serial string) *adbRunner {
	return &adbRunner{serial: serial}
}

func (a *adbRunner) cmd(args ...string) *exec.Cmd {
	if a.serial != "" {
		args = append([]string{"-s", a.serial}, args...)
	}
	return exec.Command("adb", args...)
}

func (a *adbRunner) run(args ...string) ([]byte, error) {
	cmd := a.cmd(args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("adb %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return out.Bytes(), nil
}

// findDevice returns -s, else ANDROID_SERIAL, else the only device adb can use.
func findDevice(serial string) (string, error) {
	if serial != "" {
		return serial, nil
	}
	if s := envOr("ANDROID_SERIAL", ""); s != "" {
		return s, nil
	}
	out, err := exec.Command("adb", "devices").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", errors.New("adb not found: install Android platform-tools (often packaged as android-tools or adb), or use --peer to reach the scterm Android app without it")
	}
	if err != nil {
		return "", fmt.Errorf("adb devices: %v", err)
	}
	return pickDevice(string(out))
}

// pickDevice chooses the one device `adb devices` lists as ready, or says why
// there is none: a device adb can see but not use needs something done on it.
func pickDevice(list string) (string, error) {
	var ready []string
	var notReady [][2]string // serial, state
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "*") || strings.HasPrefix(line, "List of devices") {
			continue
		}
		state := fields[1]
		// "no permissions (...udev...); see [...]", or without the udev part
		// "no permissions; see [...]" when the host has no plugdev group.
		if state == "no" && len(fields) > 2 && strings.HasPrefix(fields[2], "permissions") {
			state = "no permissions"
		}
		if state == "device" {
			ready = append(ready, fields[0])
		} else {
			notReady = append(notReady, [2]string{fields[0], state})
		}
	}
	switch {
	case len(ready) == 1:
		return ready[0], nil
	case len(ready) > 1:
		return "", fmt.Errorf("%d devices are connected (%s): choose one with -s SERIAL", len(ready), strings.Join(ready, ", "))
	case len(notReady) == 0:
		return "", errors.New("no device found: connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer for the scterm Android app")
	}
	serial, state := notReady[0][0], notReady[0][1]
	switch state {
	case "unauthorized":
		return "", fmt.Errorf("%s has not allowed USB debugging from this computer: unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again", serial)
	case "no permissions":
		return "", fmt.Errorf("adb has no permission to open %s: add a udev rule for it (or join the plugdev group), then unplug and replug it", serial)
	case "offline":
		return "", fmt.Errorf("%s is offline: unplug and replug it, or restart adb with 'adb kill-server'", serial)
	case "authorizing", "connecting":
		return "", fmt.Errorf("%s is still %s: try again in a moment", serial, state)
	default:
		return "", fmt.Errorf("%s is in %s mode, not ready for scterm: start Android on it normally", serial, state)
	}
}

func envOr(key, def string) string {
	if v, ok := lookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// pushServer pushes the embedded server jar to the device.
func (a *adbRunner) pushServer(data []byte) error {
	// Use adb push with stdin redirect for speed: push from a temp file.
	dir, err := osMkdirTemp("", "scterm-server")
	if err != nil {
		return err
	}
	defer osRemoveAll(dir)
	path := dir + "/scrcpy-server"
	if err := osWriteFile(path, data, 0o644); err != nil {
		return err
	}
	_, err = a.run("push", path, "/data/local/tmp/scrcpy-server.jar")
	return err
}

// reverse sets up the reverse tunnel (device -> host).
func (a *adbRunner) reverse(deviceSocket string, port uint16) error {
	_, err := a.run("reverse", "localabstract:"+deviceSocket, fmt.Sprintf("tcp:%d", port))
	return err
}

func (a *adbRunner) reverseRemove(deviceSocket string) error {
	_, err := a.run("reverse", "--remove", "localabstract:"+deviceSocket)
	return err
}

func (a *adbRunner) forward(port uint16, deviceSocket string) error {
	_, err := a.run("forward", fmt.Sprintf("tcp:%d", port), "localabstract:"+deviceSocket)
	return err
}

func (a *adbRunner) forwardRemove(port uint16) error {
	_, err := a.run("forward", "--remove", fmt.Sprintf("tcp:%d", port))
	return err
}

// serverVersion returns the scrcpy version declared by the vendored server.
// The server rejects mismatched versions, so we must pass exactly what the
// jar was built for.
func (a *adbRunner) serverVersion() string {
	// The system scrcpy-server at /usr/share/scrcpy is v4.1; the client must
	// pass the same version string.
	return "4.1"
}

// startServer launches the scrcpy server on the device. Its output goes to out,
// so a server that stops before connecting can say why.
func (a *adbRunner) startServer(params []string, out io.Writer) (*exec.Cmd, error) {
	args := []string{
		"shell",
		"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
		"app_process", "/", "com.genymobile.scrcpy.Server",
		a.serverVersion(),
	}
	args = append(args, params...)
	cmd := a.cmd(args...)
	cmd.Stdout = out
	cmd.Stderr = out
	// The output pipe stays open while anything adb started still holds it;
	// once adb itself has exited, Wait must not wait for those.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// lastLines returns up to n of the last non-empty lines written, leaving out
// Java stack frames: the server's error line and the exception say why it
// stopped, and a long trace would otherwise push them out.
func (t *tailBuffer) lastLines(n int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(string(t.buf), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "at ") || (strings.HasPrefix(l, "...") && strings.HasSuffix(l, " more")) {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func (a *adbRunner) getprop(name string) string {
	out, err := a.run("shell", "getprop", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// deviceDisplaySize returns cur=WxH from dumpsys window (rotated physical size).
func (a *adbRunner) deviceDisplaySize() (w, h int) {
	out, err := a.run("shell", "dumpsys", "window", "displays")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "cur=") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.HasPrefix(f, "cur=") {
					sz := strings.TrimPrefix(f, "cur=")
					parts := strings.Split(sz, "x")
					if len(parts) == 2 {
						w, _ = strconv.Atoi(parts[0])
						h, _ = strconv.Atoi(parts[1])
						return
					}
				}
			}
		}
	}
	return 0, 0
}
