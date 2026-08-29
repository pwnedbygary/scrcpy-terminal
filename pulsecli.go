package main

import (
	"os"
	"os/exec"
	"strings"
)

// pulseCmd runs the pulseaudio CLI (pactl) via the user session socket.
func pulseCmd(args ...string) ([]byte, error) {
	cmd := exec.Command("pactl", args...)
	cmd.Env = append(os.Environ(), pulseEnv()...)
	return cmd.Output()
}

func pulseEnv() []string {
	var env []string
	if d := envOr("XDG_RUNTIME_DIR", ""); d != "" {
		env = append(env, "PULSE_SERVER=unix:"+d+"/pulse/native")
	}
	return env
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, l)
	}
	return out
}

func splitFields(s string) []string { return strings.Fields(s) }

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
