package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// findDevice returns a serial for the first online device, or ANDROID_SERIAL.
func findDevice(serial string) (string, error) {
	if serial != "" {
		return serial, nil
	}
	if s := envOr("ANDROID_SERIAL", ""); s != "" {
		return s, nil
	}
	out, err := exec.Command("adb", "devices").Output()
	if err != nil {
		return "", fmt.Errorf("adb devices: %v", err)
	}
	var found []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" && !strings.HasPrefix(fields[0], "*") {
			found = append(found, fields[0])
		}
	}
	if len(found) == 0 {
		return "", errors.New("no device connected (and ANDROID_SERIAL not set)")
	}
	if len(found) > 1 {
		return "", fmt.Errorf("multiple devices: %s (pass -s SERIAL)", strings.Join(found, ", "))
	}
	return found[0], nil
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

func (a *adbRunner) startServer(params []string) (*exec.Cmd, error) {
	args := []string{
		"shell",
		"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
		"app_process", "/", "com.genymobile.scrcpy.Server",
		a.serverVersion(),
	}
	args = append(args, params...)
	cmd := a.cmd(args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
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
