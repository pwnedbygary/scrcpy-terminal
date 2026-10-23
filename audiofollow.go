package main

import (
	"time"
)

// audioFollowEvery is how often followAudioSink asks for the default sink.
// The runs read it and pass it on, before the follower's goroutine starts.
var audioFollowEvery = time.Second

// audioFollower says whether scterm's stream should follow the desktop's
// sink: only when the sink plays on this computer (it opened, has not failed
// or been closed since, and the browser is not the only output),
// SCT_AUDIO_DEVICE does not pin it, and pactl is on PATH. It returns pactl,
// with the environment as it is now. The runs call it before they start the
// follower, which then reads neither PATH nor the environment, which a test
// that started it puts back when it ends.
func (a *app) audioFollower() (pactlTool, bool) {
	if !a.audio.plays() {
		return pactlTool{}, false
	}
	if envOr("SCT_AUDIO_DEVICE", "") != "" {
		return pactlTool{}, false // explicit choice: do not follow
	}
	pactl, err := findPactl()
	return pactl, err == nil
}

// followAudioSink watches the desktop-selected default sink and moves our
// stream to it whenever the user switches output devices (KDE panel,
// `pactl set-default-sink`, ...), asking pactl every `every`. Runs until stop
// is closed, when the run that started it ends, or until sink, whose stream it
// moves, is closed, when the session's sound has stopped: a tick that begins
// after that asks pactl nothing. sink is never nil: the runs start a follower
// only for a sink that plays (audioFollower).
func followAudioSink(pactl pactlTool, sink *audioSink, stop <-chan struct{}, every time.Duration) {
	poll := time.NewTicker(every)
	defer poll.Stop()
	var lastMoved string
	for {
		select {
		case <-stop:
			return
		case <-poll.C:
		}
		select { // select picks at random when a tick and the stop are both ready
		case <-stop:
			return
		default:
		}
		if sink.closed.Load() {
			return
		}
		def := pactl.defaultSink()
		if def == "" {
			continue
		}
		ids := pactl.sctSinkInputs()
		if len(ids) == 0 {
			// our stream hasn't appeared yet (or audio is idle/silent but the
			// sink still exists - pa_simple keeps the stream open, so ids
			// should be non-empty; tolerate transient gaps)
			lastMoved = ""
			continue
		}
		if def == lastMoved {
			continue // nothing changed since the stream was last moved or found there
		}
		// Move every sct sink-input that is not on the new default yet. At
		// start the stream usually is: its sink opened on the default.
		at := pactl.sinkIndex(def) // "" when pactl does not list it
		moved, already := false, true
		for id, sink := range ids {
			if at != "" && sink == at {
				continue
			}
			already = false
			if _, err := pactl.run("move-sink-input", id, def); err == nil {
				moved = true
			}
		}
		if already {
			lastMoved = def // nothing to move, nor to say
		}
		if moved {
			lastMoved = def
			noticef("scterm: audio -> %s\n", def)
		}
	}
}
