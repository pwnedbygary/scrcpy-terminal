package main

import (
	"os"
	"os/exec"
	"strings"
)

// pactlTool is pactl as found on PATH, with the environment it runs in: the
// process's when it was found, aimed at the user session socket.
type pactlTool struct {
	path string
	env  []string
}

// findPactl looks pactl up on PATH, and takes the environment as it is now.
func findPactl() (pactlTool, error) {
	path, err := exec.LookPath("pactl")
	if err != nil {
		return pactlTool{}, err
	}
	return pactlTool{path: path, env: append(os.Environ(), pulseEnv()...)}, nil
}

func (p pactlTool) run(args ...string) ([]byte, error) {
	cmd := exec.Command(p.path, args...)
	cmd.Env = p.env
	return cmd.Output()
}

// pulseCmd runs the pulseaudio CLI (pactl) via the user session socket.
func pulseCmd(args ...string) ([]byte, error) {
	p, err := findPactl()
	if err != nil {
		return nil, err
	}
	return p.run(args...)
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

// splitFields splits a line of pactl's short listings into its columns.
// pactl separates them with tabs, and a column can hold spaces: the sample
// spec reads "s16le 2ch 48000Hz".
func splitFields(s string) []string { return strings.Split(s, "\t") }

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }

// pulseDefaultSink returns the desktop-selected default sink name (what KDE's
// Audio Volume panel selects). Empty on error.
func pulseDefaultSink() string {
	p, err := findPactl()
	if err != nil {
		return ""
	}
	return p.defaultSink()
}

func (p pactlTool) defaultSink() string {
	out, err := p.run("get-default-sink")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sctSinkInputs parses `pactl list sink-inputs` and returns the sink-input ids
// whose application.name is "scterm" (our streams).
func (p pactlTool) sctSinkInputs() []string {
	out, err := p.run("list", "sink-inputs")
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
