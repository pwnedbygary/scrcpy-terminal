package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// notices: runtime messages while the terminal UI owns the screen
//
// A line printed on the alternate screen lands wherever the cursor is and
// scrolls the picture (the row-diff renderer then leaves rows shifted), and
// leaving the alternate screen at exit throws it away. So while the terminal
// UI runs, a message is shown in the status row instead, in place of the
// state and stats after the hints, for noticeShowFor (the newest wins), and
// kept to be printed on stderr once the terminal is back (the notices about a
// paste are not kept: see pasteNoticef). The status row is
// used rather than a row of its own because it is already painted on its own
// and at once (tui.setStatus), while every row above it belongs to the
// picture and the overlays (the action bar and the keyboard sit at the
// bottom). Before the terminal UI starts, after it ends and in the modes
// without one, a message goes to stderr at once.
//
// Messages come from several goroutines. The board only records them: the
// status row is painted by the run loop, which the board wakes.
// ---------------------------------------------------------------------------

const (
	// noticeShowFor is how long a notice stays in the status row; the run
	// loop's 500 ms tick takes it down.
	noticeShowFor = 4 * time.Second
	// noticeReplayMax is how many different lines the exit replay keeps; the
	// oldest go first and are counted.
	noticeReplayMax = 50
)

type noticeBoard struct {
	mu      sync.Mutex
	active  bool          // the terminal UI owns the screen
	wake    chan struct{} // asks the run loop to repaint the status row
	text    string        // the notice on show, cleaned for the status row
	until   int64         // when it stops showing (unix nanoseconds)
	replay  []noticeLine  // in order of first appearance
	dropped int           // lines pushed out of replay
}

// noticeLine is one line of the replay and how many times it came.
type noticeLine struct {
	text  string
	count int
}

var notices noticeBoard

// notice reports msg, a whole stderr line ("scterm: ...\n"): on stderr, or,
// while the terminal UI runs, in its status row and the replay at exit.
func notice(msg string) {
	notices.post(msg, timeNowUnixNano())
}

// noticef is notice with formatting.
func noticef(format string, args ...any) {
	notice(fmt.Sprintf(format, args...))
}

// pasteNoticef is noticef for a notice about a paste, which the replay at exit
// leaves out: it would leave a paste's length (a password's, say) in the
// shell's scrollback. It still shows in the status row.
func pasteNoticef(format string, args ...any) {
	notices.show(fmt.Sprintf(format, args...), timeNowUnixNano())
}

// post shows msg as the notice and keeps it for the replay, or prints it on
// stderr when the terminal UI is not running.
func (b *noticeBoard) post(msg string, now int64) { b.put(msg, now, true) }

// show is post for a message the replay leaves out.
func (b *noticeBoard) show(msg string, now int64) { b.put(msg, now, false) }

// put shows msg as the notice, keeping it for the replay if keep, or prints
// it on stderr when the terminal UI is not running. Printing under the lock
// keeps a message from reaching the alternate screen while start or stop runs.
func (b *noticeBoard) put(msg string, now int64, keep bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		fmt.Fprint(stderrWriter(), msg)
		return
	}
	line := strings.TrimRight(msg, "\n")
	b.text = cleanText(strings.TrimPrefix(line, "scterm: "))
	b.until = now + int64(noticeShowFor)
	if keep {
		b.keep(line)
	}
	select {
	case b.wake <- struct{}{}:
	default: // a repaint is already on its way
	}
}

// keep adds line to the replay, counting a repeat instead of keeping it twice.
func (b *noticeBoard) keep(line string) {
	for i := range b.replay {
		if b.replay[i].text == line {
			b.replay[i].count++
			return
		}
	}
	if len(b.replay) == noticeReplayMax {
		b.replay = append(b.replay[:0], b.replay[1:]...)
		b.dropped++
	}
	b.replay = append(b.replay, noticeLine{text: line, count: 1})
}

// start sends messages to the terminal UI, until stop. The channel it returns
// is signalled when the status row has a new notice to show.
func (b *noticeBoard) start() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = true
	if b.wake == nil {
		b.wake = make(chan struct{}, 1)
	}
	return b.wake
}

// current is the notice to show at now, or "" when there is none.
func (b *noticeBoard) current(now int64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active || now >= b.until {
		return ""
	}
	return b.text
}

// stop sends messages to stderr again, and prints to w those the terminal UI
// showed, each line once with its count, so an error or the path of a saved
// screenshot outlives the alternate screen.
func (b *noticeBoard) stop(w io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return
	}
	switch {
	case b.dropped == 1:
		fmt.Fprintln(w, "scterm: 1 earlier message not shown")
	case b.dropped > 1:
		fmt.Fprintf(w, "scterm: %d earlier messages not shown\n", b.dropped)
	}
	for _, l := range b.replay {
		if l.count > 1 {
			fmt.Fprintf(w, "%s (%d times)\n", l.text, l.count)
		} else {
			fmt.Fprintln(w, l.text)
		}
	}
	b.active, b.text, b.until, b.replay, b.dropped = false, "", 0, nil, 0
}
