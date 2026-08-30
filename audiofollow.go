package main

import (
	"fmt"
	"time"
)

// followAudioSink watches the desktop-selected default sink and moves our
// stream to it whenever the user switches output devices (KDE panel,
// `pactl set-default-sink`, ...). Runs until the app exits.
//
// Only runs when the sink was opened and SCT_AUDIO_DEVICE is not pinned.
func (a *app) followAudioSink() {
	if a.audio == nil || a.audio.err != nil {
		return
	}
	if envOr("SCT_AUDIO_DEVICE", "") != "" {
		return // explicit choice: do not follow
	}
	poll := time.NewTicker(1 * time.Second)
	defer poll.Stop()
	var lastMoved string
	for range poll.C {
		def := pulseDefaultSink()
		if def == "" {
			continue
		}
		ids := sctSinkInputs()
		if len(ids) == 0 {
			// our stream hasn't appeared yet (or audio is idle/silent but the
			// sink still exists - pa_simple keeps the stream open, so ids
			// should be non-empty; tolerate transient gaps)
			lastMoved = ""
			continue
		}
		if def == lastMoved {
			continue // nothing changed since the last successful move
		}
		// Move every sct sink-input to the new default.
		moved := false
		for _, id := range ids {
			if _, err := pulseCmd("move-sink-input", id, def); err == nil {
				moved = true
			}
		}
		if moved {
			lastMoved = def
			logOnce(fmt.Sprintf("scterm: audio -> %s\n", def))
		}
	}
}
