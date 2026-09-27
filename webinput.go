package main

// Browser-facing control API.
//
// Both display modes (--web and --window) funnel every control event through
// one dispatcher goroutine, so device messages are serialized in the order the
// user produced them and a burst of input never queues behind a frame encode.
//
// Event ops (JSON, client -> server):
//
//	hello   {w,h,format,caps}   negotiate viewport + payload format
//	resize  {w,h}               viewport changed
//	ping                        ask for a status message
//	down|move|up {x,y}          pointer, x/y are 0..65535 fractions of the canvas
//	wheel   {x,y,delta}         wheel notches, positive = up (content up)
//	key     {code,meta}         Android keycode + metastate (down+up)
//	text    {text}              UTF-8/ASCII to type into the device
//	back                        Back key / wake screen
//	home, menu, appswitch, power, volup, voldown, mute, notif, settings, collapse
//	rotate                      rotate the device
//	resetvideo                  ask the device for a keyframe
//	gain    {gain}              local (browser) volume, percent
//	quit                        end the session

import (
	"fmt"
	"unicode/utf8"
)

// webInbox is the bounded queue of client control events. A slow device must
// not let the queue grow without bound; the oldest pointer event is the least
// valuable thing in it, so overflow drops from the front.
const webInbox = 512

func (s *webServer) dispatch(c *webClient, ev *webEvent) {
	if s.app == nil {
		// Headless (--web --no-tui): no app loop to serialize against, so
		// apply immediately from the socket reader. Same order, less latency.
		s.applyControl(c, ev)
		return
	}
	if s.inbox == nil {
		return
	}
	select {
	case s.inbox <- webCmd{client: c, ev: *ev}:
	default:
		s.logLimited("scterm: web: input queue full, dropping %q\n", ev.Op)
	}
}

// runDispatcher applies queued control events in order.
func (s *webServer) runDispatcher() {
	for {
		select {
		case <-s.done:
			return
		case cmd := <-s.inbox:
			s.applyControl(cmd.client, &cmd.ev)
		}
	}
}

// applyControl performs one control event against the app/device. c may be
// nil in headless mode, where there is no app to drive.
func (s *webServer) applyControl(c *webClient, ev *webEvent) {
	a := s.app
	if a == nil {
		s.applyEventHeadless(c, ev)
		return
	}
	switch ev.Op {
	case "down":
		a.webGrab()
		pos := c.devicePos(ev)
		a.drag.setDown(true)
		s.send(func() { s.touch(true, pos) })
	case "move":
		if a.drag.isDown() {
			a.coalescedMove(c.devicePos(ev))
		}
	case "up":
		pos := c.devicePos(ev)
		// Clearing the press also drops any move still queued in the
		// coalescer, so a release is never followed by a stale move.
		a.drag.setDown(false)
		s.send(func() { s.touch(false, pos) })
	case "wheel":
		delta := ev.Delta
		if delta == 0 {
			delta = 1
		}
		pos := c.devicePos(ev)
		s.send(func() { s.scroll(pos, delta) })
	case "key":
		s.send(func() { a.sendAndroidKeyMeta(ev.Code, ev.Meta) })
		if isVolumeKey(ev.Code) {
			s.kickVolume()
		}
	case "text":
		a.webInput(ev.Text)
	case "back", "home", "menu", "appswitch", "power", "volup", "voldown",
		"mute", "notif", "settings", "collapse", "rotate", "resetvideo", "grab":
		// One shared dispatch with the terminal's F-keys (appkeys.go), so F5 in
		// the browser and F5 in the TUI are the same action by construction.
		op := ev.Op
		s.send(func() { a.applyAppOp(op) })
		if isVolumeKey(appKeyOps[op]) {
			s.kickVolume()
		}
	case "gain":
		if a == nil {
			break
		}
		if a.audio != nil && a.audio.err == nil {
			g := ev.Gain
			if ev.Mute != nil {
				if *ev.Mute {
					g = 0
				} else if a.audio.gainPercent() == 0 {
					g = 100
				}
			}
			a.audio.setGain(g)
			a.refreshStatus()
		}
		if c != nil {
			c.sendStatus()
		}
	case "quit":
		a.quitWeb()
	}
}

// applyEventHeadless handles control events for --web --no-tui runs, where
// there is no app loop: the server talks to the controller directly.
func (s *webServer) applyEventHeadless(c *webClient, ev *webEvent) {
	switch ev.Op {
	case "down":
		s.touch(true, c.devicePos(ev))
	case "move":
		if s.ctrl != nil {
			_ = s.ctrl.touchMove(c.devicePos(ev))
		}
	case "up":
		s.touch(false, c.devicePos(ev))
	case "wheel":
		d := ev.Delta
		if d == 0 {
			d = 1
		}
		s.scroll(c.devicePos(ev), d)
	case "key":
		if s.ctrl != nil {
			_ = s.ctrl.injectKey(ev.Code, ev.Meta)
		}
	case "text":
		s.typeHeadless(ev.Text)
	case "back":
		if s.ctrl != nil {
			_ = s.ctrl.back()
		}
	case "home", "menu", "appswitch", "power", "volup", "voldown", "mute":
		if s.ctrl != nil {
			_ = s.ctrl.injectKey(appKeyOps[ev.Op], 0)
		}
		if ev.Op == "volup" || ev.Op == "voldown" || ev.Op == "mute" {
			s.kickVolume()
		}
	case "notif":
		if s.ctrl != nil {
			_ = s.ctrl.expandNotifications()
		}
	case "settings":
		if s.ctrl != nil {
			_ = s.ctrl.expandSettings()
		}
	case "collapse":
		if s.ctrl != nil {
			_ = s.ctrl.collapsePanels()
		}
	case "rotate":
		if s.ctrl != nil {
			_ = s.ctrl.rotate()
		}
	case "resetvideo":
		if s.ctrl != nil {
			_ = s.ctrl.resetVideo()
		}
	case "quit":
		s.quit()
	}
}

// typeHeadless types text with no app loop: letters/digits/space go as key
// events (games respond to those), everything else as injected text.
func (s *webServer) typeHeadless(text string) {
	if s.ctrl == nil || text == "" {
		return
	}
	var buf []byte
	flush := func() {
		if len(buf) > 0 {
			_ = s.ctrl.injectText(string(buf))
			buf = buf[:0]
		}
	}
	for _, r := range text {
		if r < 0x80 {
			if code, meta, ok := asciiKey(byte(r)); ok {
				flush()
				_ = s.ctrl.injectKey(code, meta)
				continue
			}
		}
		buf = utf8.AppendRune(buf, r)
	}
	flush()
}

// headlessKey is the Android keycode for an app-level key op; the table lives
// in appkeys.go so the terminal's F-keys and the browser's ops stay identical.
func headlessKey(op string) uint32 {
	return appKeyOps[op]
}

// ---------------------------------------------------------------------------
// small app-side helpers used by the control API
// ---------------------------------------------------------------------------

// send runs a device write on the control goroutine. It never blocks the
// dispatcher: control writes to a local socket are microseconds.
func (s *webServer) send(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderrWriter(), "scterm: web: control: %v\n", r)
		}
	}()
	fn()
}

func (s *webServer) touch(down bool, pos position) {
	if s.ctrl == nil {
		return
	}
	if down {
		_ = s.ctrl.touch(true, pos)
		return
	}
	_ = s.ctrl.touchRelease(pos)
}

func (s *webServer) scroll(pos position, delta int) {
	if s.ctrl == nil {
		return
	}
	_ = s.ctrl.scroll(pos, float32(delta), 0)
}

// isVolumeKey reports whether an Android keycode can change the media volume,
// so the HUD's device-volume reading can be refreshed right after injection
// instead of waiting for the next poll. 91 (the microphone MUTE) stays in the
// set so a raw key event carrying it still refreshes; the app's own mute
// action sends 164.
func isVolumeKey(code uint32) bool {
	switch code {
	case 24, 25, 91, 164: // VOLUME_UP, VOLUME_DOWN, MUTE, VOLUME_MUTE
		return true
	}
	return false
}

// webGrab makes sure the device sees pointer events even when the app started
// believing the mouse was "ungrabbed" (that flag only means "terminal mouse
// reporting off", which does not apply to a browser).
func (a *app) webGrab() {
	if a.grabbed {
		return
	}
	a.grabbed = true
	a.refreshStatus()
}

// backPress presses Back exactly as the terminal does: a full down+up pair via
// ctrl.back(), which the scrcpy server also turns into a screen wake when the
// display is off. Sending only the key-up (as this used to) reaches the device
// and is then ignored, so Back appeared to do nothing in the browser.
func (a *app) backPress() {
	if a.ctrl == nil {
		return
	}
	_ = a.ctrl.back()
}

func (a *app) expandNotifications() {
	if a.ctrl != nil {
		_ = a.ctrl.expandNotifications()
	}
}

func (a *app) expandSettings() {
	if a.ctrl != nil {
		_ = a.ctrl.expandSettings()
	}
}

func (a *app) collapsePanels() {
	if a.ctrl != nil {
		_ = a.ctrl.collapsePanels()
	}
}

func (a *app) rotateDevice() {
	if a.ctrl != nil {
		_ = a.ctrl.rotate()
	}
}

func (a *app) resetVideo() {
	if a.ctrl != nil {
		_ = a.ctrl.resetVideo()
	}
}

// webInput types text into the device. Single letters/digits/space go as key
// events (games and IMEs respond to them); everything else is injected as
// text, which is the only way to send punctuation and non-ASCII reliably.
func (a *app) webInput(text string) {
	if a.ctrl == nil || text == "" {
		return
	}
	// Split into runs: key-mappable ASCII chars go one by one, the rest is
	// batched into injectText calls.
	var buf []byte
	flush := func() {
		if len(buf) == 0 {
			return
		}
		_ = a.ctrl.injectText(string(buf))
		buf = buf[:0]
	}
	for _, r := range text {
		if r < 0x80 {
			c := byte(r)
			if code, meta, ok := asciiKey(c); ok {
				flush()
				a.sendAndroidKeyMeta(code, meta)
				continue
			}
		}
		buf = utf8.AppendRune(buf, r)
	}
	flush()
}

// asciiKey maps one ASCII byte to an Android keycode (letters, digits and
// space), mirroring the terminal byte handler.
func asciiKey(c byte) (uint32, uint32, bool) {
	switch {
	case c == ' ':
		return 62, 0, true // SPACE
	case c >= 'a' && c <= 'z':
		return uint32(29 + c - 'a'), 0, true
	case c >= 'A' && c <= 'Z':
		return uint32(29 + c - 'A'), metaShiftOn, true
	case c >= '0' && c <= '9':
		return uint32(7 + c - '0'), 0, true
	}
	return 0, 0, false
}

// quitWeb ends a --web/--window session from a client request.
func (a *app) quitWeb() {
	if a.web != nil {
		a.web.stop()
	}
	select {
	case a.events <- inputEvent{kind: evQuit}:
	default:
	}
}

// webCmd is one queued control event.
type webCmd struct {
	client *webClient
	ev     webEvent
}
