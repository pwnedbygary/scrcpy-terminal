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

// pulseDefaultSink returns the desktop-selected default sink name (what KDE's
// Audio Volume panel selects). Empty on error.
func pulseDefaultSink() string {
	out, err := pulseCmd("get-default-sink")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sctSinkInputs parses `pactl list sink-inputs` and returns the sink-input ids
// whose application.name is "scterm" (our streams).
func sctSinkInputs() []string {
	out, err := pulseCmd("list", "sink-inputs")
	if err != nil {
		return nil
	}
	var ids []string
	var curID string
	for _, line := range splitLines(string(out)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "Sink Input #") {
			curID = strings.TrimPrefix(line, "Sink Input #")
			continue
		}
		if curID != "" && strings.HasPrefix(trimmed, "application.name") && strings.Contains(trimmed, `"scterm"`) {
			ids = append(ids, curID)
			curID = ""
		}
	}
	return ids
}
