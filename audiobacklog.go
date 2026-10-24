package main

import (
	"bufio"
	"net"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Where this computer plays the sound, the audio demuxer reads, decodes and
// plays it on one goroutine, and each write waits for room in the sound
// server's 20 ms buffer (writePCM16), so the demuxer reads the sound only as
// fast as it plays. What the device sends faster waits in the demuxer's buffer
// and in the socket. Over wireless adb, 0.2 to 0.6 s of sound already waits
// when the first packet is read; with a video playing, both streams stop for 1
// to 6 s, a few times a minute, and then come in a burst; and even without
// stalls the sound comes in bursts of up to about a second. Played at its
// normal speed and never dropped, all of that would stay queued, and the sound
// would trail the picture by it for the rest of the session. audioBacklog
// drops what stays.
const (
	// backlogWindow is how long, in sound played, the queue must stay above
	// what it keeps before the rest goes: long enough to hold two or more of
	// the bursts, which come up to about a second apart, and short enough
	// that a stall's backlog goes within seconds of the stall.
	backlogWindow = 2500 * time.Millisecond
	// backlogSegment is how far the window slides at a time.
	backlogSegment = backlogWindow / 5
	// backlogMargin is the sound left queued at the window's low point
	// after a drop.
	backlogMargin = 150 * time.Millisecond
	// backlogStep is the least that is dropped: less is left queued.
	backlogStep = 100 * time.Millisecond
	// backlogBurst is the largest rise and fall of the queue within a
	// window that a cushion is kept for. Bursts reached about a second; a
	// larger swing is a stall, which only a cushion as long as the stall
	// would bridge.
	backlogBurst = 1200 * time.Millisecond
	// backlogMemory is how long, by the device's stamps, the cushion for a
	// burst is kept: bursts that needed a second of it came back within half
	// a minute.
	backlogMemory = 30 * time.Second
)

// audioBacklog decides which audio packets to drop unplayed (skip). Only the
// audio demuxer's goroutine uses it, apart from dropped and cushion.
type audioBacklog struct {
	// window holds the last whole segments of sound played, oldest first;
	// whole says how many there are since the window last started.
	window [backlogWindow / backlogSegment]queueSpan
	whole  int
	seg    queueSpan // the segment being filled
	inSeg  bool

	// swings holds the rise and fall of the queue over the window at
	// segment ends, in a ring whose next slot is next. Segment ends are at
	// least backlogSegment of stamps apart, so the ring outlasts
	// backlogMemory. A window that holds a stall, overlaps one or starts
	// before the last one ended has no entry (see skip).
	swings [backlogMemory / backlogSegment]windowSwing
	next   int
	// stallEnd is the stamp at the end of the last window that held a stall.
	stallEnd time.Duration

	// A drop in progress skips the packets stamped from skipFrom to skipTo.
	skipping         bool
	skipFrom, skipTo time.Duration

	// dropped is the sound dropped in all, and cushion the bursts' cushion as
	// of the last whole window; the statistics line of --no-tui reads them
	// from another goroutine.
	dropped, cushion atomic.Int64
}

// queueSpan is how low and how high the queue was over the sound played from
// the packet stamped start.
type queueSpan struct{ start, lo, hi time.Duration }

// windowSwing is a window's rise and fall, and the stamp it ended at.
type windowSwing struct{ end, swing time.Duration }

// skip takes the packet stamped pts, with queued waiting behind it, and
// reports whether to drop it unplayed. The queue's low point over the last
// backlogWindow of sound played is sound that stayed queued all that time. Of
// it, skip keeps backlogMargin, and the cushion that the bursts of the last
// backlogMemory needed beyond this window's own bursts. When that leaves
// backlogStep or more, skip drops as much of the oldest sound, from this
// packet on, and the window starts over after it. A drop never takes the last
// packet queued.
//
// A stall's refill can take a second or two to arrive, so the windows around
// the one whose swing shows the stall may each see part of its rise, a swing
// that looks like a burst's. None of them is kept: the windows before that
// overlap it lose their entries, and the windows after record nothing until
// one starts after the last window that held the stall.
func (b *audioBacklog) skip(pts, queued time.Duration) bool {
	if b.skipping {
		if pts < b.skipTo && queued > 0 {
			return true
		}
		b.stopSkipping(pts)
	}
	if !b.inSeg {
		b.seg, b.inSeg = queueSpan{pts, queued, queued}, true
		return false
	}
	b.seg.lo, b.seg.hi = min(b.seg.lo, queued), max(b.seg.hi, queued)
	if pts-b.seg.start < backlogSegment {
		return false
	}
	copy(b.window[:], b.window[1:])
	b.window[len(b.window)-1] = b.seg
	b.whole = min(b.whole+1, len(b.window))
	b.seg = queueSpan{pts, queued, queued}
	if b.whole < len(b.window) {
		return false
	}
	lo, hi := b.window[0].lo, b.window[0].hi
	for _, s := range b.window[1:] {
		lo, hi = min(lo, s.lo), max(hi, s.hi)
	}
	swing, start := hi-lo, b.window[0].start
	if swing > backlogBurst {
		for i := range b.swings {
			if b.swings[i].end > start {
				b.swings[i] = windowSwing{}
			}
		}
		b.stallEnd = pts
	} else if start >= b.stallEnd {
		b.swings[b.next] = windowSwing{pts, swing}
		b.next = (b.next + 1) % len(b.swings)
	}
	var cushion time.Duration
	for _, s := range b.swings {
		if pts-s.end < backlogMemory {
			cushion = max(cushion, s.swing)
		}
	}
	b.cushion.Store(int64(cushion))
	excess := lo - backlogMargin - max(cushion-swing, 0)
	if excess < backlogStep {
		return false
	}
	b.skipping, b.skipFrom, b.skipTo = true, pts, pts+excess
	b.whole, b.inSeg = 0, false
	return true
}

// stopSkipping ends a drop at the packet stamped pts, which plays.
func (b *audioBacklog) stopSkipping(pts time.Duration) {
	b.skipping = false
	b.dropped.Add(int64(pts - b.skipFrom))
}

// restart forgets the window while the sink's writes do not wait for the sound
// to play (paces): the packet stamped pts plays.
func (b *audioBacklog) restart(pts time.Duration) {
	if b.skipping {
		b.stopSkipping(pts)
	}
	b.whole, b.inSeg = 0, false
}

// audioQueued is the sound that waits behind the packet stamped ptsUs: the
// stamp of the newest whole media packet in r, less ptsUs. What waits in the
// socket rc is read into r first, so that the queue holds every packet that
// has come from the device. rc is nil when the socket's queue cannot be read;
// then only r counts.
func audioQueued(r *bufio.Reader, rc syscall.RawConn, ptsUs int64) time.Duration {
	if n := socketUnread(rc); n > 0 {
		r.Peek(min(r.Buffered()+n, r.Size()))
	}
	buf, _ := r.Peek(r.Buffered())
	newest := ptsUs
	for len(buf) >= 12 {
		isSession, pts, isConfig, _, size := parsePacketHeader(buf[:12])
		if len(buf)-12 < int(size) {
			break
		}
		if !isSession && !isConfig {
			newest = max(newest, pts)
		}
		buf = buf[12+int(size):]
	}
	return time.Duration(newest-ptsUs) * time.Microsecond
}

// rawConn is c's socket, for socketUnread, or nil. The audio of a peer session
// comes through TLS, which reads ahead a record at a time, so its queue cannot
// be read.
func rawConn(c net.Conn) syscall.RawConn {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return nil
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return nil
	}
	return rc
}

// socketUnread is how many bytes wait to be read in the socket rc, or 0 when
// it cannot say.
func socketUnread(rc syscall.RawConn) int {
	if rc == nil {
		return 0
	}
	n := 0
	rc.Control(func(fd uintptr) {
		var v int32
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCINQ, uintptr(unsafe.Pointer(&v))); e == 0 && v > 0 {
			n = int(v)
		}
	})
	return n
}
