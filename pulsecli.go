package main

import (
	"os"
	"os/exec"
	"strings"
)

// pactlTool is pactl as found on PATH, with the environment it runs in: the
// process's when it was found, aimed at the user session socket, in the C
// locale (cLocaleEnv).
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
	cmd.Env = cLocaleEnv(p.env)
	return cmd.Output()
}

// cLocaleEnv is env in the C locale, for a program whose output scterm reads.
// pactl translates what it prints with gettext (the "Sink:" of its listings,
// the field names of pactl info), and in the C locale gettext translates
// nothing: LC_ALL=C overrides LANG and the other LC_ variables. gettext then
// ignores LANGUAGE too; it goes all the same, so that the output does not
// depend on that rule. Names and properties come out of pactl byte for byte
// the same in the C locale as in any other. The rest of env stays.
func cLocaleEnv(env []string) []string {
	kept := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name != "LC_ALL" && name != "LANGUAGE" {
			kept = append(kept, kv)
		}
	}
	return append(kept, "LC_ALL=C")
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
// whose application.name is "scterm" (our streams), each with the index of the
// sink it plays on ("" when the listing does not say).
func (p pactlTool) sctSinkInputs() map[string]string {
	out, err := p.run("list", "sink-inputs")
	if err != nil {
		return nil
	}
	ids := map[string]string{}
	var curID, curSink string
	for _, line := range splitLines(string(out)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "Sink Input #") {
			curID, curSink = strings.TrimPrefix(line, "Sink Input #"), ""
			continue
		}
		if sink, ok := strings.CutPrefix(trimmed, "Sink: "); ok {
			curSink = sink
		}
		if curID != "" && strings.HasPrefix(trimmed, "application.name") && strings.Contains(trimmed, `"scterm"`) {
			ids[curID] = curSink
			curID = ""
		}
	}
	return ids
}

// sinkIndex returns the index of the sink named name, from `pactl list sinks
// short`, or "" when pactl does not list it.
func (p pactlTool) sinkIndex(name string) string {
	out, err := p.run("list", "sinks", "short")
	if err != nil {
		return ""
	}
	for _, line := range splitLines(string(out)) {
		if fields := splitFields(line); len(fields) >= 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}
