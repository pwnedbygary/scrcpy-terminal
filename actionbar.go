package main

// ---------------------------------------------------------------------------
// action bar: the terminal twin of the web page's button strip
//
// The browser gets a floating bar of buttons; the terminal gets the same thing
// drawn with the software keyboard's pill spans (dark gray background, light
// text) in the last row of the video area, just above the status line. Like
// the browser bar it appears when the mouse is active and fades after a few
// seconds, so it never sits on top of the picture while you are watching.
//
// Buttons dispatch through menuAction, the same function the action menu and
// the F-keys use: a click and a chord run the identical code path.
// ---------------------------------------------------------------------------

import "time"

const (
	// barIdle is how long the bar stays after the last mouse activity,
	// matching the web page's HUD cadence.
	barIdle = 2500 * time.Millisecond
	// barScrollStep shifts the window when the bar is wider than the pane.
	barScrollStep = 8
	// barMinWidth is the smallest pane the bar will draw in.
	barMinWidth = 16
)

// barItem is one clickable button: the menu item plus its compact label.
type barItem struct {
	menuItem
	Short string
}

var barItems = []barItem{
	{menuItem{'h', "home", "Home"}, "Home"},
	{menuItem{'b', "back", "Back"}, "Back"},
	{menuItem{'t', "appswitch", "Recents"}, "Rec"},
	{menuItem{0, opMenuKey, "Menu key"}, "Menu"},
	{menuItem{'u', "volup", "Volume up"}, "Vol+"},
	{menuItem{'d', "voldown", "Volume down"}, "Vol-"},
	{menuItem{0, "mute", "Mute"}, "Mute"},
	{menuItem{'r', "rotate", "Rotate"}, "Rot"},
	{menuItem{'n', "notif", "Notifications"}, "Notif"},
	{menuItem{'e', "settings", "Settings shade"}, "Shade"},
	{menuItem{'c', "collapse", "Collapse panels"}, "Collapse"},
	{menuItem{'p', "power", "Power"}, "Power"},
	{menuItem{'i', opKeyboard, "Keyboard"}, "Keys"},
	{menuItem{'s', "screenshot", "Screenshot"}, "Shot"},
	{menuItem{'k', "resetvideo", "Keyframe"}, "Refresh"},
	{menuItem{'g', "grab", "Grab"}, "Grab"},
	{menuItem{'q', "quit", "Quit"}, "Quit"},
}

// barHit is one clickable region of the rendered bar: rune columns (0-based,
// [from,to)) on the bar's overlay line. scroll != 0 marks the ‹ / › edges;
// otherwise idx indexes barItems.
type barHit struct {
	from, to int
	idx      int
	scroll   int
}

// barItemAt returns the item for a non-scroll hit.
func barItemAt(h barHit) barItem { return barItems[h.idx] }

// barVisible reports whether the bar should be drawn: requested by mouse
// activity, with a terminal, and no other overlay owning the screen.
func (a *app) barVisible() bool {
	return a.barShow && a.tui != nil && !a.menuOpen && (a.kb == nil || !a.kb.open)
}

// wakeBar shows the bar and restarts its idle timer. Mouse motion keeps it up;
// no repaint happens when it is already visible. Other overlays own the screen
// while they are open, so motion under them is ignored.
func (a *app) wakeBar() {
	if a.tui == nil || a.menuOpen || (a.kb != nil && a.kb.open) {
		return
	}
	a.barLastActivity = timeNowUnixNano()
	if a.barShow {
		return
	}
	a.barShow = true
	a.refreshOverlays()
}

// barTick hides the bar once it has been idle long enough. Called from the
// run loop's ticks; a no-op while the bar is hidden.
func (a *app) barTick() {
	a.barHideCheck(timeNowUnixNano())
}

// barFlashDuration is how long a clicked button stays green.
const barFlashDuration = 350 * time.Millisecond

// barHideCheck is barTick with an explicit clock, so it can be tested without
// sleeping.
func (a *app) barHideCheck(now int64) {
	// Clear a finished click flash even while the bar stays up.
	if a.barFlashAt != 0 && now-a.barFlashAt >= int64(barFlashDuration) {
		a.barFlashIdx = -1
		a.barFlashAt = 0
		a.refreshOverlays()
	}
	if !a.barShow {
		return
	}
	if now-a.barLastActivity < int64(barIdle) {
		return
	}
	a.barShow = false
	a.barFlashIdx = -1
	a.barFlashAt = 0
	a.barHits = nil
	a.refreshOverlays()
}

// barLayout packs the bar into at most width rune cells starting at the
// current offset. It never mutates state: scrolling is the click handler's
// job.
func (a *app) barLayout(width int) (string, []barHit, bool, bool) {
	return a.barLayoutAt(a.barOffset, width)
}

// barLayoutAt is barLayout for an explicit offset, so barLine can ask whether
// the unscrolled bar now fits the pane.
func (a *app) barLayoutAt(offset, width int) (string, []barHit, bool, bool) {
	if width < barMinWidth {
		return "", nil, false, false
	}
	left := offset > 0
	// Reserve one rune for a possible right arrow, so a full row still has
	// room for it and nothing is drawn past the pane.
	limit := width
	if left {
		limit -= 2 // "‹ "
	}
	limit--

	text := make([]rune, 0, width)
	var hits []barHit
	used := 0
	add := func(s string) (int, int) {
		from := used
		for _, r := range s {
			text = append(text, r)
			used++
		}
		return from, used
	}
	if left {
		from, to := add("‹ ")
		hits = append(hits, barHit{from: from, to: to, scroll: -1})
	}
	right := false
	for i := offset; i < len(barItems); i++ {
		it := barItems[i]
		w := len([]rune(it.Short)) + 2 // one pad each side
		if len(hits) > 0 {
			w++ // the gap before the button
		}
		if used+w > limit {
			right = i < len(barItems) // anything left at all is off-screen
			break
		}
		if len(hits) > 0 {
			add(" ")
		}
		from, to := add(" " + it.Short + " ")
		hits = append(hits, barHit{from: from, to: to, idx: i})
	}
	if right {
		from, to := add("›")
		hits = append(hits, barHit{from: from, to: to, scroll: +1})
	}
	return string(text), hits, left, right
}

// barLine renders the bar's overlay line, or nil when it must not draw.
// Called from refreshOverlays during painting, so it must be free of
// side effects other than recording the hit regions for the next click.
func (a *app) barLine() *overlayLine {
	if !a.barVisible() {
		return nil
	}
	width, _ := termSize()
	text, hits, _, _ := a.barLayout(width)
	if text == "" {
		return nil
	}
	// A wider pane can make the whole bar fit without scrolling; snap back so
	// a stale offset does not pin a needless "‹" on screen.
	if a.barOffset > 0 {
		if full, fullHits, _, fullRight := a.barLayoutAt(0, width); full != "" && !fullRight {
			a.barOffset = 0
			text, hits = full, fullHits
		}
	}
	a.barHits = hits
	segs := make([]overlaySeg, 0, len(hits))
	for _, h := range hits {
		code := btnBg
		switch {
		case h.scroll != 0:
			code = barScrollStyle
		case a.barFlashAt != 0 && h.idx == a.barFlashIdx:
			// The just-clicked button, until barHideCheck clears it. The
			// timestamp is required: an app built without newApp has
			// barFlashIdx 0, and without it Home would flash on first paint.
			code = btnBgFlash
		}
		segs = append(segs, overlaySeg{from: h.from, to: h.to, code: code})
	}
	return &overlayLine{text: text, segs: segs}
}

// barClick handles a mouse event on the bar's row. Returns true when it was
// consumed (an action ran, or the event was a click on the strip), false when
// the caller should treat it as device input.
//
// pressed distinguishes the press edge from the release edge: only the press
// runs an action. A release is swallowed only when no device touch is in
// flight -- after a press on the bar there is none -- so a drag that started
// on the video and ended over the bar still sends its touch-release.
func (a *app) barClick(cellX, cellY, rows int, pressed bool) bool {
	if !a.barVisible() || cellY != rows-1 {
		return false
	}
	if !pressed {
		return !a.drag.isDown()
	}
	col := cellX - 1
	for _, h := range a.barHits {
		if col < h.from || col >= h.to {
			continue
		}
		if h.scroll != 0 {
			a.barOffset += h.scroll * barScrollStep
			if a.barOffset < 0 {
				a.barOffset = 0
			}
			if a.barOffset > len(barItems)-1 {
				a.barOffset = len(barItems) - 1
			}
			a.barFlashIdx = -1
			a.refreshOverlays()
			return true
		}
		a.barFlashIdx = h.idx
		a.barFlashAt = timeNowUnixNano()
		a.menuAction(barItemAt(h).Op)
		a.refreshOverlays()
		return true
	}
	// The strip itself: a press below the last button must not tap the video.
	return true
}

const barScrollStyle = "\x1b[48;2;40;40;48m\x1b[38;2;180;180;190m"

// refreshOverlays repaints the overlay and status line from current state.
// This is the single writer of the overlay slot: the menu, the software
// keyboard and the action bar are mutually exclusive by construction, so none
// of them can leave another's stale layout on screen (which used to let a
// click run a button that was no longer painted).
func (a *app) refreshOverlays() {
	if a.tui == nil {
		return
	}
	_, rows := termSize()
	var lines []overlayLine
	switch {
	case a.menuOpen:
		lines = padLines(a.menuLines(rows), menuLeftMargin)
	case a.kb != nil && a.kb.open:
		lines = a.kb.lines(rows)
	default:
		if bl := a.barLine(); bl != nil {
			lines = make([]overlayLine, rows)
			idx := rows - 2 // last video row; rows-1 is the status line
			if idx < 0 {
				idx = 0
			}
			if idx < len(lines) {
				lines[idx] = *bl
			}
		} else {
			a.barHits = nil
		}
	}
	if lines != nil {
		a.tui.setOverlay(lines)
	} else {
		a.tui.setOverlay(nil)
	}
	a.tui.setStatus(a.statusLine())
	a.tui.markDirty()
}
